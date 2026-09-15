package app

import (
	"encoding/json"
	"testing"
	"time"
)

// Waiting on the agent with nothing on screen reads the same as the agent being
// stuck. So a session says while it is working — a turn running or queued — and
// for how long, both as it changes and when the page is opened mid-turn.

func sessionWorking(t *testing.T, a *App, id string) *float64 {
	t.Helper()
	w := call(t, a, "GET", "/sessions/"+id, "")
	var got struct {
		Session Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return got.Session.WorkingSeconds
}

// kindsUntil reads this session's events until one of the given kind arrives.
func kindsUntil(t *testing.T, events chan wsEvent, sessionID, last string) []string {
	t.Helper()
	var kinds []string
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-events:
			if e.SessionID != sessionID || (e.Kind != "working" && e.Kind != "idle") {
				continue
			}
			kinds = append(kinds, e.Kind)
			if e.Kind == last {
				return kinds
			}
		case <-deadline:
			t.Fatalf("no %q event; saw %v", last, kinds)
		}
	}
}

func TestASessionSaysWhileItIsWorkingAndForHowLong(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionWorking(t, a, s.ID); got != nil {
		t.Fatalf("an idle session reports working for %vs", *got)
	}
	events := a.hub.add()
	defer a.hub.remove(events)

	release := make(chan struct{})
	started := make(chan struct{})
	a.enqueue(s.ID, func() { close(started); <-release })
	// A second turn queued behind the first keeps the session working: the
	// operator is waiting on both.
	a.enqueue(s.ID, func() {})
	<-started

	if kinds := kindsUntil(t, events, s.ID, "working"); len(kinds) != 1 {
		t.Errorf("events before working = %v", kinds)
	}
	if got := sessionWorking(t, a, s.ID); got == nil || *got < 0 {
		t.Errorf("a working session does not say for how long: %v", got)
	}

	close(release)
	if kinds := kindsUntil(t, events, s.ID, "idle"); len(kinds) != 1 {
		t.Errorf("the session went idle between two queued turns: %v", kinds)
	}
	if got := sessionWorking(t, a, s.ID); got != nil {
		t.Errorf("a finished session still reports working for %vs", *got)
	}
}
