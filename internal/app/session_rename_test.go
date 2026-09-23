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
