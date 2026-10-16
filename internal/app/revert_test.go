package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// revertTo asks the API to take a conversation back to before the entry at seq.
func revertTo(t *testing.T, a *App, id string, seq int) *httptest.ResponseRecorder {
	t.Helper()
	return callAPI(t, a, "POST", "/sessions/"+id+"/revert", map[string]any{"seq": seq})
}

// seqOf is the sequence number of the first user message carrying text.
func seqOf(t *testing.T, a *App, id, text string) int {
	t.Helper()
	for _, e := range a.store.Entries(id) {
		if e.Type == "message" && e.Role == "user" && e.Text == text {
			return e.Seq
		}
	}
	t.Fatalf("no user message %q in %s", text, id)
	return -1
}

// The criterion: after a revert the model is sent everything before the message
// reverted to and nothing from it onwards, and the transcript only grew.
func TestARevertTakesBackTheMessageAndEverythingAfterIt(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "again, then")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s := newSession(t, a)
	growTurns(t, a, s, 3)
	before := transcriptOf(t, a, s.ID)

	w := revertTo(t, a, s.ID, seqOf(t, a, s.ID, "question-02"))
	if w.Code != 200 {
		t.Fatalf("revert: status %d (%s)", w.Code, w.Body.String())
	}

	after := transcriptOf(t, a, s.ID)
	if len(after) != len(before)+1 {
		t.Fatalf("a revert wrote %d entries, want exactly one", len(after)-len(before))
	}
	for i, e := range before {
		x, _ := json.Marshal(e)
		y, _ := json.Marshal(after[i])
		if string(x) != string(y) {
			t.Fatalf("a revert changed entry %d on disk:\nwas %s\nnow %s", i, x, y)
		}
	}
	last := after[len(after)-1]
	if last.Type != "event" || last.EventKind != "revert" || last.RevertsFrom != seqOf(t, a, s.ID, "question-02") {
		t.Fatalf("the revert is not recorded as a revert event naming where it cut: %+v", last)
	}

	if w := callAPI(t, a, "POST", "/sessions/"+s.ID+"/messages", map[string]any{"text": "question-02, put better"}); w.Code != 202 {
		t.Fatalf("send: status %d (%s)", w.Code, w.Body.String())
	}
	settle(t, a, s.ID)
	req := lastRequest(t, seen)
	for _, kept := range []string{"question-01", "answer-01", "question-02, put better"} {
		if !strings.Contains(req, kept) {
			t.Errorf("%s was not taken back, but the model was not sent it", kept)
		}
	}
	for _, gone := range []string{`"question-02"`, "answer-02", "question-03", "answer-03"} {
		if strings.Contains(req, gone) {
			t.Errorf("%s was taken back, and the model was still sent it", gone)
		}
	}
}

// What a revert takes back is still on disk and still found, and the hit says
// the agent no longer sees it.
func TestWhatARevertTookBackIsStillFoundBySearch(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	growTurns(t, a, s, 3)
	if w := revertTo(t, a, s.ID, seqOf(t, a, s.ID, "question-03")); w.Code != 200 {
		t.Fatalf("revert: status %d (%s)", w.Code, w.Body.String())
	}

	find := func(q string) SearchHit {
		t.Helper()
		w := callAPI(t, a, "GET", "/search?q="+q+"&session="+s.ID, nil)
		if w.Code != 200 {
			t.Fatalf("search: status %d (%s)", w.Code, w.Body.String())
		}
		var hits []SearchHit
		if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 {
			t.Fatalf("search for %s found %d entries, want 1", q, len(hits))
		}
		return hits[0]
	}
	if h := find("answer-03"); !h.Reverted {
		t.Errorf("a hit on an entry the revert took back does not say so: %+v", h)
	}
	if h := find("answer-02"); h.Reverted {
		t.Errorf("a hit on an entry before the revert says it was taken back: %+v", h)
	}
}

// A compaction written after the message reverted to is taken back with it.
// The one before it is in force again, and what that one had not folded is
// sent word for word.
func TestARevertTakesBackACompactionWrittenSince(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "## Decisions\n- FIRST-SUMMARY")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	s.CompactAtTokens = 1 << 20
	s.KeepVerbatimTokens = 1200
	_ = a.store.PutSession(s)
	growTurns(t, a, s, 12)
	if err := a.Compact(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "question-13"})
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "answer-13"})
	// An edit is a second compaction, written after question-13.
	if w := callAPI(t, a, "PUT", "/sessions/"+s.ID+"/compaction", map[string]any{"text": "## Decisions\n- EDITED-SUMMARY"}); w.Code != 200 {
		t.Fatalf("edit: status %d (%s)", w.Code, w.Body.String())
	}

	if w := revertTo(t, a, s.ID, seqOf(t, a, s.ID, "question-13")); w.Code != 200 {
		t.Fatalf("revert: status %d (%s)", w.Code, w.Body.String())
	}
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: "go on"}); err != nil {
		t.Fatal(err)
	}
	req := lastRequest(t, seen)
	if strings.Contains(req, "EDITED-SUMMARY") || !strings.Contains(req, "FIRST-SUMMARY") {
		t.Error("the summary edited after the message reverted to is still the one in force")
	}
	if strings.Contains(req, "question-13") || strings.Contains(req, "answer-13") {
		t.Error("the turn reverted is still sent")
	}
	if !strings.Contains(req, "question-12") || strings.Contains(req, "question-01") {
		t.Error("the compaction back in force does not carry what it carried")
	}

	// Back past the first compaction too: nothing is folded, and the turns it
	// had folded are sent again.
	if w := revertTo(t, a, s.ID, seqOf(t, a, s.ID, "question-12")); w.Code != 200 {
		t.Fatalf("revert: status %d (%s)", w.Code, w.Body.String())
	}
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: "from the top"}); err != nil {
		t.Fatal(err)
	}
	req = lastRequest(t, seen)
	if strings.Contains(req, "FIRST-SUMMARY") {
		t.Error("a compaction written after the message reverted to is still sent")
	}
	if !strings.Contains(req, "question-01") || !strings.Contains(req, "answer-11") {
		t.Error("the turns the reverted compaction had folded are not sent again")
	}
	if strings.Contains(req, "question-12") || strings.Contains(req, "go on") {
		t.Error("what the second revert took back is still sent")
	}
}

// The cut is only ever where a turn starts. Anywhere else would leave a call
// without its result, so it is refused, and nothing is written.
func TestARevertIsRefusedUnlessItNamesAUserMessageStillThere(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	growTurns(t, a, s, 3)
	at := seqOf(t, a, s.ID, "question-03")
	if w := revertTo(t, a, s.ID, at); w.Code != 200 {
		t.Fatalf("revert: status %d (%s)", w.Code, w.Body.String())
	}
	before := len(transcriptOf(t, a, s.ID))

	refuse := map[string]int{
		"an assistant message":       seqOf(t, a, s.ID, "question-02") + 1,
		"a tool result":              seqOf(t, a, s.ID, "question-02") + 2,
		"the prompt":                 0,
		"a message already reverted": at,
		"no entry at all":            999,
		"a revert itself":            before - 1,
	}
	for what, seq := range refuse {
		w := revertTo(t, a, s.ID, seq)
		if w.Code != 400 {
			t.Errorf("a revert to %s: status %d, want 400 (%s)", what, w.Code, w.Body.String())
		}
	}
	if w := callAPI(t, a, "POST", "/sessions/"+s.ID+"/revert", map[string]any{}); w.Code != 400 {
		t.Errorf("a revert naming nothing: status %d, want 400", w.Code)
	}
	if w := revertTo(t, a, "NOSUCHSESSION", 1); w.Code != 404 {
		t.Errorf("a revert of a session that does not exist: status %d, want 404", w.Code)
	}
	if after := len(transcriptOf(t, a, s.ID)); after != before {
		t.Errorf("a refused revert wrote %d entries", after-before)
	}
}

// A wake starts a turn as a typed message does, so it is a place to cut too.
func TestAConversationCanBeRevertedToBeforeAWake(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "Remind me hourly."})
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "Scheduled."})
	wake := a.append(s.ID, Entry{Type: "message", Role: "user", Text: "Remind the operator.", JobID: "J1"})
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "WAKE-REPLY"})
	if w := revertTo(t, a, s.ID, wake.Seq); w.Code != 200 {
		t.Fatalf("revert to a wake: status %d (%s)", w.Code, w.Body.String())
	}
	for _, m := range Project(a.store.Entries(s.ID)) {
		if strings.Contains(contentOf(m), "WAKE-REPLY") || strings.Contains(contentOf(m), "Remind the operator.") {
			t.Fatalf("the reverted wake is still projected: %s", contentOf(m))
		}
	}
}

// A revert while a turn is under way is refused: the turn would go on writing
// after it, answering a message that is no longer there.
func TestARevertWhileWorkingIsRefused(t *testing.T) {
	a := newTestApp(t)
	release := make(chan struct{})
	srv := slowModel(t, release)
	t.Cleanup(srv.Close)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s := newSession(t, a)
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "first"})
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "one"})
	if w := callAPI(t, a, "POST", "/sessions/"+s.ID+"/messages", map[string]any{"text": "second"}); w.Code != 202 {
		t.Fatalf("send: status %d (%s)", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for !a.Busy(s.ID) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !a.Busy(s.ID) {
		t.Fatal("the session never started working")
	}

	w := revertTo(t, a, s.ID, seqOf(t, a, s.ID, "first"))
	close(release)
	settle(t, a, s.ID)
	if w.Code != 409 {
		t.Fatalf("a revert while working: status %d, want 409 (%s)", w.Code, w.Body.String())
	}
	for _, e := range transcriptOf(t, a, s.ID) {
		if e.EventKind == "revert" {
			t.Fatal("a refused revert was written anyway")
		}
	}
}

// A revert is of the conversation and nothing else. What the turns did to the
// working directory and the schedule stays done.
func TestARevertLeavesTheFilesAndJobsAsTheyWere(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "Write the notes and remind me."})
	file := filepath.Join(a.ensureWorkspace(s.ID), "notes.txt")
	if err := os.WriteFile(file, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "1h", Prompt: "Remind the operator."})
	if err != nil {
		t.Fatal(err)
	}
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "Done both."})

	if w := revertTo(t, a, s.ID, seqOf(t, a, s.ID, "Write the notes and remind me.")); w.Code != 200 {
		t.Fatalf("revert: status %d (%s)", w.Code, w.Body.String())
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "kept" {
		t.Errorf("a revert touched the working directory: %q, %v", b, err)
	}
	if got, err := a.store.Job(j.ID); err != nil || got == nil || got.Status != j.Status {
		t.Errorf("a revert touched the conversation's job: %+v, %v", got, err)
	}
}

// What the provider cached ended where the conversation did before the revert,
// which is not where the next call ends, so the session stops claiming it.
func TestARevertClearsTheCacheTheSessionReports(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	growTurns(t, a, s, 2)
	if _, err := a.store.UpdateSession(s.ID, func(x *Session) {
		x.Cache = &SessionCache{Tokens: 5000, ExpiresAt: time.Now().Add(time.Hour)}
	}); err != nil {
		t.Fatal(err)
	}
	if sessionCache(t, a, s.ID).Session.Cache == nil {
		t.Fatal("setup: the session reports no cache to clear")
	}
	if w := revertTo(t, a, s.ID, seqOf(t, a, s.ID, "question-02")); w.Code != 200 {
		t.Fatalf("revert: status %d (%s)", w.Code, w.Body.String())
	}
	if c := sessionCache(t, a, s.ID).Session.Cache; c != nil {
		t.Errorf("after a revert the session still reports a cache of %d tokens", c.Tokens)
	}
}

// A fork copies a reverted conversation as the agent reads it: the revert is
// renumbered with the copy, so what it took back stays taken back.
func TestAForkOfARevertedConversationSendsWhatItsOriginSends(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "noted")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	origin := newSession(t, a)
	growTurns(t, a, origin, 3)
	if w := revertTo(t, a, origin.ID, seqOf(t, a, origin.ID, "question-02")); w.Code != 200 {
		t.Fatalf("revert: status %d (%s)", w.Code, w.Body.String())
	}
	w := callAPI(t, a, "POST", "/sessions/"+origin.ID+"/fork", map[string]any{})
	if w.Code != 201 {
		t.Fatalf("fork: status %d (%s)", w.Code, w.Body.String())
	}
	var fork Session
	if err := json.Unmarshal(w.Body.Bytes(), &fork); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{origin.ID, fork.ID} {
		if w := callAPI(t, a, "POST", "/sessions/"+id+"/messages", map[string]any{"text": "Which is right?"}); w.Code != 202 {
			t.Fatalf("send to %s: status %d (%s)", id, w.Code, w.Body.String())
		}
		settle(t, a, id)
	}
	if len(*seen) < 2 {
		t.Fatalf("expected a call from each session, got %d", len(*seen))
	}
	texts := func(body string) string {
		var req struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range req.Messages {
			if m["role"] == "system" {
				continue
			}
			c, _ := json.Marshal(m["content"])
			out = append(out, fmt.Sprint(m["role"], ": ", string(c)))
		}
		return strings.Join(out, "\n")
	}
	want, got := texts((*seen)[0]), texts((*seen)[1])
	if got != want {
		t.Errorf("the fork sent a different conversation from its origin\norigin: %s\nfork:   %s", want, got)
	}
	if strings.Contains(got, "answer-02") {
		t.Error("the fork sent what its origin's revert took back")
	}
}
