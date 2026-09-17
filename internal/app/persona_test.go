package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// A persona is the opening section of the system prompt, chosen before a session
// starts and fixed for its life. Choosing one replaces the built-in whole.

func TestASessionThatChoseNoPersonaRunsOnTheBuiltInOne(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	text, ok := promptSection(t, a, s, "persona")
	if !ok {
		t.Fatal("the prompt has no persona section")
	}
	if text != persona {
		t.Errorf("a session that chose nothing is not on the built-in persona:\n%s", text)
	}
}

func TestAChosenPersonaReplacesTheBuiltInOneWhole(t *testing.T) {
	a := newTestApp(t)
	w := callAPI(t, a, "POST", "/personas", map[string]any{
		"name": "terse-reviewer", "description": "Reviews code and says little.",
		"body": "You are a terse code reviewer. You do not schedule anything.",
	})
	if w.Code != 200 {
		t.Fatalf("POST /personas: status %d (%s)", w.Code, w.Body.String())
	}

	s, err := a.NewSession(SessionConfig{Model: "test/model", Persona: "terse-reviewer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	text, ok := promptSection(t, a, s, "persona")
	if !ok {
		t.Fatal("the prompt has no persona section")
	}
	if !strings.Contains(text, "terse code reviewer") {
		t.Errorf("the chosen persona is not in the prompt: %q", text)
	}
	// Full replacement, as chosen: nothing of the built-in persona survives,
	// including its working rules.
	if strings.Contains(text, "single-user autonomous agent") {
		t.Errorf("the built-in persona is still there beside the chosen one:\n%s", text)
	}
}

// A persona named in a session that no longer exists on disk must not leave the
// agent with no persona at all.
func TestAPersonaThatIsGoneFallsBackToTheBuiltInAndSaysSo(t *testing.T) {
	a := newTestApp(t)
	callAPI(t, a, "POST", "/personas", map[string]any{
		"name": "temporary", "description": "Briefly.", "body": "You are brief.",
	})
	s, err := a.NewSession(SessionConfig{Model: "test/model", Persona: "temporary"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := callAPI(t, a, "DELETE", "/personas/temporary", nil); w.Code != 204 {
		t.Fatalf("DELETE: status %d (%s)", w.Code, w.Body.String())
	}
	// A session already running keeps the prompt it has; a new one on the same
	// configuration is the case this is about.
	next, err := a.NewSession(s.SessionConfig, "")
	if err != nil {
		t.Fatal(err)
	}
	text, _ := promptSection(t, a, next, "persona")
	if !strings.Contains(text, "single-user autonomous agent") {
		t.Errorf("a missing persona left the agent without one:\n%s", text)
	}
	if !strings.Contains(text, "no longer on disk") {
		t.Errorf("the fallback does not say what happened:\n%s", text)
	}
}

func TestTheBuiltInPersonaIsListedAndCannotBeEditedOrDeleted(t *testing.T) {
	a := newTestApp(t)
	w := callAPI(t, a, "GET", "/personas", nil)
	var list struct {
		Personas []struct {
			Name     string `json:"name"`
			Editable bool   `json:"editable"`
		} `json:"personas"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if len(list.Personas) != 1 || list.Personas[0].Name != defaultPersona {
		t.Fatalf("personas = %+v, want just the built-in one", list.Personas)
	}
	if list.Personas[0].Editable {
		t.Error("the built-in persona reports itself as editable")
	}

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/personas/default", map[string]any{"description": "d", "body": "Mine now."}},
		{"DELETE", "/personas/default", nil},
		{"POST", "/personas", map[string]any{"name": "default", "description": "d", "body": "Mine."}},
	} {
		if w := callAPI(t, a, c.method, c.path, c.body); w.Code != 409 {
			t.Errorf("%s %s: status %d, want 409 (%s)", c.method, c.path, w.Code, w.Body.String())
		}
	}
	if a.personas.Text("") != persona {
		t.Error("the built-in persona was changed")
	}
}

// The persona is part of the configuration, so it moves the only way a
// configuration moves.
func TestThePersonaIsFixedForASessionAndChangesByForking(t *testing.T) {
	a := newTestApp(t)
	callAPI(t, a, "POST", "/personas", map[string]any{
		"name": "terse", "description": "Says little.", "body": "You are terse.",
	})
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}

	w := callAPI(t, a, "PATCH", "/sessions/"+s.ID, map[string]any{"persona": "terse"})
	if w.Code != 409 {
		t.Fatalf("PATCH persona: status %d, want 409 (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "fork") {
		t.Errorf("the refusal does not name forking: %s", w.Body.String())
	}

	cfg := s.SessionConfig
	cfg.Persona = "terse"
	fork, err := a.Fork(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := promptSection(t, a, fork, "persona"); !strings.Contains(text, "You are terse") {
		t.Errorf("the fork is not on the persona it was forked onto: %q", text)
	}
	// Both transcripts say what changed.
	for _, id := range []string{s.ID, fork.ID} {
		var said bool
		for _, e := range a.store.Entries(id) {
			if strings.Contains(e.Text, "persona default → terse") {
				said = true
			}
		}
		if !said {
			t.Errorf("session %s does not record the persona change", id)
		}
	}
}
