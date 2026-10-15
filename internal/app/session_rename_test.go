package app

import (
	"encoding/json"
	"testing"
)

// A title is a label, not configuration: nothing in the prompt reads it, so it
// is the one thing about a session that stays open for the session's whole
// life. Renaming goes over the same PATCH that refuses the model, the tools,
// the skills, and the grants.
func TestSessionIsRenamed(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "x/y"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := callAPI(t, a, "PATCH", "/sessions/"+s.ID, map[string]any{"title": "Tomato bed telemetry"}); w.Code != 200 {
		t.Fatalf("PATCH title: status %d, want 200 (%s)", w.Code, w.Body.String())
	}
	w := callAPI(t, a, "GET", "/sessions/"+s.ID, nil)
	if w.Code != 200 {
		t.Fatalf("GET session: status %d (%s)", w.Code, w.Body.String())
	}
	var got struct {
		Session struct {
			Title string `json:"title"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Session.Title != "Tomato bed telemetry" {
		t.Errorf("title after renaming is %q, want %q", got.Session.Title, "Tomato bed telemetry")
	}
}

// The title a person chose is theirs: a session that has one is not retitled
// by the model when its first message lands.
func TestChosenTitleSurvivesTheFirstTurn(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "x/y"}, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Title = "Tomato bed telemetry"
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}
	// titleIfNeeded makes a model call when it has work to do; here it must
	// make none, so a nil client is the assertion.
	a.titleIfNeeded(t.Context(), s, "what is the soil moisture")
	if s.Title != "Tomato bed telemetry" {
		t.Errorf("a chosen title was overwritten with %q", s.Title)
	}
}

func sessionTitle(t *testing.T, a *App, id string) string {
	t.Helper()
	var got struct {
		Session struct {
			Title string `json:"title"`
		} `json:"session"`
	}
	if err := json.Unmarshal(callAPI(t, a, "GET", "/sessions/"+id, nil).Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Session.Title
}

// A request that is refused changes nothing, even the part of it that would
// have been allowed on its own. A rename sent with a new model used to be
// refused for the model and applied for the title, in memory, and written to
// disk by whatever saved the session next.
func TestARefusedChangeChangesNothing(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	before := sessionTitle(t, a, s.ID)
	if w := callAPI(t, a, "PATCH", "/sessions/"+s.ID, map[string]any{"title": "Renamed", "model": "other/model"}); w.Code != 409 {
		t.Fatalf("PATCH title with model: status %d, want 409 (%s)", w.Code, w.Body.String())
	}
	if got := sessionTitle(t, a, s.ID); got != before {
		t.Errorf("a refused PATCH renamed the session to %q", got)
	}
	// What a later save writes is what the session holds, so a change left in
	// memory would reach disk here.
	if w := callAPI(t, a, "PATCH", "/sessions/"+s.ID, map[string]any{"compact_at_tokens": 50000}); w.Code != 200 {
		t.Fatalf("PATCH thresholds: status %d (%s)", w.Code, w.Body.String())
	}
	reopened := newTestAppAt(t, a.cfg.DataDir)
	if got := reopened.store.Session(s.ID).Title; got != before {
		t.Errorf("the refused rename reached disk as %q", got)
	}
}

// A conversation with no title is harder to find than one with a stale title,
// so a blank is refused rather than stored, by the agent and not only by the
// screen that sends it.
func TestABlankTitleIsRefused(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	before := sessionTitle(t, a, s.ID)
	for _, blank := range []string{"", "   ", "\n\t"} {
		if w := callAPI(t, a, "PATCH", "/sessions/"+s.ID, map[string]any{"title": blank}); w.Code != 400 {
			t.Errorf("PATCH title %q: status %d, want 400 (%s)", blank, w.Code, w.Body.String())
		}
	}
	if got := sessionTitle(t, a, s.ID); got != before {
		t.Errorf("a blank rename left the title %q", got)
	}
	if w := callAPI(t, a, "PATCH", "/sessions/"+s.ID, map[string]any{"title": "  Kiln log  "}); w.Code != 200 {
		t.Fatalf("PATCH title: status %d (%s)", w.Code, w.Body.String())
	}
	if got := sessionTitle(t, a, s.ID); got != "Kiln log" {
		t.Errorf("title after renaming is %q, want it trimmed to %q", got, "Kiln log")
	}
}
