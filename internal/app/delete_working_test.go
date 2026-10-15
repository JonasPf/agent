package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// A conversation deleted while it is working is deleted: its turn stops where
// it is, and nothing the turn would have written afterwards brings it back. It
// used to finish its model call, write its answer into the place the delete had
// just emptied, and reappear in the list as two entries with no prompt.
func TestDeletingAWorkingConversationEndsItForGood(t *testing.T) {
	a := newTestApp(t)
	asked := make(chan struct{}, 1)
	release := make(chan struct{})
	cancelled := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read to the end, or the server never watches the connection and
		// cannot see the caller hang up.
		_, _ = io.Copy(io.Discard, r.Body)
		asked <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			cancelled <- struct{}{}
			return
		case <-time.After(10 * time.Second):
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"content": "an answer nobody can see"}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s := newSession(t, a)
	if w := callAPI(t, a, "POST", "/sessions/"+s.ID+"/messages", map[string]any{"text": "Write the report."}); w.Code != 202 {
		t.Fatalf("send: status %d (%s)", w.Code, w.Body.String())
	}
	select {
	case <-asked:
	case <-time.After(3 * time.Second):
		t.Fatal("the model was never called")
	}

	if w := callAPI(t, a, "DELETE", "/sessions/"+s.ID, nil); w.Code != 204 {
		t.Fatalf("delete: status %d (%s)", w.Code, w.Body.String())
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Error("deleting a working conversation left its model call running")
	}
	settle(t, a, s.ID)

	if w := callAPI(t, a, "GET", "/sessions/"+s.ID, nil); w.Code != 404 {
		t.Errorf("GET the deleted session: status %d, want 404 (%s)", w.Code, w.Body.String())
	}
	var list []Session
	if err := json.Unmarshal(callAPI(t, a, "GET", "/sessions", nil).Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == s.ID {
			t.Error("the deleted session is back in the list")
		}
	}
	if _, err := os.Stat(a.store.sessionDir(s.ID)); !os.IsNotExist(err) {
		t.Errorf("the deleted session's directory is back on disk (stat err %v)", err)
	}
	if hits := callAPI(t, a, "GET", "/search?q=answer", nil).Body.String(); strings.Contains(hits, s.ID) {
		t.Errorf("the deleted session is searchable again: %s", hits)
	}
}

// Whatever reaches a deleted session late — a turn that was already past its
// last check, a job that fired as the delete landed — is refused by the store
// rather than written into a fresh directory under the old identifier.
func TestAWriteToADeletedSessionIsRefused(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	if err := a.DeleteSession(s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Append(s.ID, Entry{Type: "message", Role: "assistant", Text: "late"}); err == nil {
		t.Error("an entry was appended to a deleted session")
	}
	if _, err := a.store.AppendAll(s.ID, []Entry{{Type: "message", Role: "assistant", Text: "late"}}); err == nil {
		t.Error("entries were appended to a deleted session")
	}
	if err := a.store.PutSession(s); err == nil {
		t.Error("a deleted session was written back")
	}
	if a.store.Session(s.ID) != nil || len(a.store.Entries(s.ID)) != 0 {
		t.Error("a late write left the deleted session in memory")
	}
	if _, err := os.Stat(a.store.sessionDir(s.ID)); !os.IsNotExist(err) {
		t.Errorf("a late write put the deleted session's directory back (stat err %v)", err)
	}
}

// A job's wake is the session's turn too. Deleted under a wake, the session
// takes its job with it: the wake used to put the job back as it finished, and
// the job then failed every tick against a session that no longer existed,
// counting toward the breaker that stops every job in the agent.
func TestDeletingAConversationMidWakeLeavesNoJob(t *testing.T) {
	a := newTestApp(t)
	release := make(chan struct{})
	srv := slowModel(t, release)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	a.sched = NewScheduler(a)

	s := newSession(t, a)
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m", Prompt: "Has anything changed?"})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}
	a.sched.tick(t.Context())
	deadline := time.Now().Add(2 * time.Second)
	for !a.Busy(s.ID) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !a.Busy(s.ID) {
		t.Fatal("the wake never started")
	}

	if w := callAPI(t, a, "DELETE", "/sessions/"+s.ID, nil); w.Code != 204 {
		t.Fatalf("delete: status %d (%s)", w.Code, w.Body.String())
	}
	settle(t, a, s.ID)

	jobs, err := a.store.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range jobs {
		if x.SessionID == s.ID {
			t.Errorf("the deleted session's job is back: %+v", x)
		}
	}
	if runs, _ := a.store.JobRuns(j.ID); len(runs) != 0 {
		t.Errorf("the deleted session's job logged %d runs after it went", len(runs))
	}
	if a.sched.State().Open {
		t.Error("deleting a session mid-wake opened the breaker")
	}
}
