package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// unreadOf is what the session list says, read the way the interface reads it.
func unreadOf(t *testing.T, a *App, id string) int {
	t.Helper()
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions", nil))
	var list []Session
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	for _, s := range list {
		if s.ID == id {
			return s.Unread
		}
	}
	t.Fatalf("session %s is not in the list", id)
	return 0
}

func markRead(t *testing.T, a *App, id, body string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/sessions/"+id+"/read", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code >= 300 {
		t.Fatalf("mark read: status %d: %s", w.Code, w.Body.String())
	}
}

func sendMessage(t *testing.T, a *App, id, text string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"text": text})
	r := httptest.NewRequest("POST", "/sessions/"+id+"/messages", strings.NewReader(string(b)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code >= 300 {
		t.Fatalf("send: status %d: %s", w.Code, w.Body.String())
	}
	settle(t, a, id)
}

// A reminder is the case the count exists for: the agent wrote while nobody
// was looking. It has to count the same as a reply to something typed.
func TestAJobWakeCountsAsUnread(t *testing.T) {
	a, _ := modelBackedApp(t, "Reminder: feed the cat.")
	a.sched = NewScheduler(a)
	s := newSession(t, a)
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Remind me to feed the cat.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	if got := unreadOf(t, a, s.ID); got != 1 {
		t.Errorf("unread after a job wake = %d, want 1", got)
	}
}

// The count is of messages, not of turns: two answers waiting are two.
func TestEachAssistantMessageCountsOnce(t *testing.T) {
	a, _ := modelBackedApp(t, "First.", "Second.")
	s := newSession(t, a)
	sendMessage(t, a, s.ID, "one")
	sendMessage(t, a, s.ID, "two")
	if got := unreadOf(t, a, s.ID); got != 2 {
		t.Errorf("unread = %d, want 2", got)
	}
	markRead(t, a, s.ID, "")
	if got := unreadOf(t, a, s.ID); got != 0 {
		t.Errorf("unread after reading = %d, want 0", got)
	}
}

// Reading clears what the operator was shown, and nothing written after it. A
// message that lands between the page loading and the read arriving has not
// been seen, and must not be cleared by a read that did not include it.
func TestAReadClearsOnlyWhatWasShown(t *testing.T) {
	a, _ := modelBackedApp(t, "First.", "Second.")
	s := newSession(t, a)
	sendMessage(t, a, s.ID, "one")
	shown := a.store.Entries(s.ID)
	through := shown[len(shown)-1].Seq
	sendMessage(t, a, s.ID, "two")

	markRead(t, a, s.ID, `{"through":`+itoa(through)+`}`)
	if got := unreadOf(t, a, s.ID); got != 1 {
		t.Errorf("unread = %d, want the one message written after the read point", got)
	}

	// A late read for an older point does not bring back what is already read.
	markRead(t, a, s.ID, "")
	markRead(t, a, s.ID, `{"through":1}`)
	if got := unreadOf(t, a, s.ID); got != 0 {
		t.Errorf("an older read point made messages unread again: %d", got)
	}
}

// The session names the point it was read through, so the interface can open a
// conversation at the first message the operator has not seen.
func TestTheSessionSaysWhereReadingStopped(t *testing.T) {
	a, _ := modelBackedApp(t, "First.", "Second.")
	s := newSession(t, a)
	sendMessage(t, a, s.ID, "one")
	markRead(t, a, s.ID, "")
	read := a.store.Entries(s.ID)
	sendMessage(t, a, s.ID, "two")

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+s.ID, nil))
	var res struct {
		Session Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if want := read[len(read)-1].Seq; res.Session.ReadThrough != want {
		t.Errorf("read_through = %d, want %d", res.Session.ReadThrough, want)
	}
}

// A fork is made by the operator from a conversation they are reading; what it
// copies is not news to them.
func TestAForkStartsRead(t *testing.T) {
	a, _ := modelBackedApp(t, "First.")
	s := newSession(t, a)
	sendMessage(t, a, s.ID, "one")
	fork, err := a.Fork(s, s.SessionConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got := unreadOf(t, a, fork.ID); got != 0 {
		t.Errorf("a fork starts with %d unread, want 0", got)
	}
}

// Sessions written before the read point existed stored a count. An upgrade
// must neither clear it nor turn the whole history unread.
func TestAStoredCountSurvivesTheUpgrade(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	srv := fakeModel(t, "First.", "Second.", "Third.")
	t.Cleanup(srv.Close)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	s := newSession(t, a)
	for _, m := range []string{"one", "two", "three"} {
		sendMessage(t, a, s.ID, m)
	}

	meta := filepath.Join(dir, "sessions", s.ID, "meta.json")
	b, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "read_through")
	raw["unread"] = 2
	b, _ = json.Marshal(raw)
	if err := os.WriteFile(meta, b, 0o644); err != nil {
		t.Fatal(err)
	}

	again := newTestAppAt(t, dir)
	if got := unreadOf(t, again, s.ID); got != 2 {
		t.Errorf("unread after upgrade = %d, want the stored 2", got)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// readAnswer marks a session read and returns what the agent said the read did.
func readAnswer(t *testing.T, a *App, id, body string) bool {
	t.Helper()
	r := httptest.NewRequest("POST", "/sessions/"+id+"/read", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("mark read: status %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Cleared *bool `json:"cleared"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Cleared == nil {
		t.Fatalf("a read does not say whether it cleared anything: %q", w.Body.String())
	}
	return *res.Cleared
}

// listMoves counts the events already sent that send a page to read the
// session list again. A broadcast is delivered before the handler returns, so
// what has not arrived by now was never sent.
func listMoves(events chan wsEvent) int {
	n := 0
	for {
		select {
		case e := <-events:
			if e.Kind == "sessions" {
				n++
			}
		default:
			return n
		}
	}
}

// Every page showing a conversation reads each entry as it arrives, and each
// read used to send every page back for the whole session list — the costliest
// thing the agent serves — whether or not any count had moved. A read that
// clears nothing changes nothing any page shows, so it says so and tells no one.
func TestAReadThatClearsNothingMovesNoList(t *testing.T) {
	a, _ := modelBackedApp(t, "First.")
	s := newSession(t, a)
	sendMessage(t, a, s.ID, "one")
	events := a.hub.add()
	defer a.hub.remove(events)

	if !readAnswer(t, a, s.ID, "") {
		t.Error("a read that cleared an unread message said it cleared nothing")
	}
	if n := listMoves(events); n != 1 {
		t.Errorf("a read that cleared a count told pages %d times, want once", n)
	}

	if readAnswer(t, a, s.ID, "") {
		t.Error("reading the same point again said it cleared something")
	}
	if n := listMoves(events); n != 0 {
		t.Errorf("a read that cleared nothing sent pages to the list %d times", n)
	}

	// Past a tool result and an event the read point moves, but no count does:
	// only what the agent said is ever unread.
	a.append(s.ID, Entry{Type: "message", Role: "tool", ToolName: "clock", ToolResult: json.RawMessage(`{"ok":true}`)})
	a.appendEvent(s.ID, Entry{EventKind: "job_check", Text: "check said no"})
	listMoves(events)
	if readAnswer(t, a, s.ID, "") {
		t.Error("reading past a tool result and an event said it cleared something")
	}
	if n := listMoves(events); n != 0 {
		t.Errorf("a read past nothing unread sent pages to the list %d times", n)
	}
	read := a.store.Entries(s.ID)
	if got, want := a.store.Session(s.ID).ReadThrough, read[len(read)-1].Seq; got != want {
		t.Errorf("read_through = %d, want %d: the point still moves when no count does", got, want)
	}
}
