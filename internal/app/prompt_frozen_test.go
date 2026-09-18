package app

import (
	"context"
	"strings"
	"testing"
)

// A session's prompt is written once, when the session is created, and is the
// prompt for the rest of that session's life (ADR-055). Nothing rewrites it:
// not a compaction, and not an operator editing the persona it was built from.
//
// Before ADR-055 a compaction re-read the persona and the skills index in order
// to refresh memory into the prompt, so editing a persona on disk silently
// changed what a running conversation was being told at its next fold.

// The criterion: a prompt entry's persona text is the one in force when it was
// written, and editing the persona afterwards changes no existing session.
func TestEditingAPersonaDoesNotReachARunningSession(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- kept going")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	if w := callAPI(t, a, "POST", "/personas", map[string]any{
		"name": "gardener", "description": "Tends the beds.",
		"body": "You are a gardener. You speak of soil.",
	}); w.Code != 200 {
		t.Fatalf("POST /personas: status %d (%s)", w.Code, w.Body.String())
	}

	s, err := a.NewSession(SessionConfig{Model: "test/model", Persona: "gardener"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := promptSection(t, a, s, "persona"); !strings.Contains(text, "speak of soil") {
		t.Fatalf("the session did not start on its persona: %q", text)
	}

	// The operator rewrites the persona while the conversation is running.
	if w := callAPI(t, a, "PUT", "/personas/gardener", map[string]any{
		"description": "Tends the beds.",
		"body":        "You are a gardener. You speak of DRAINAGE.",
	}); w.Code != 200 {
		t.Fatalf("PUT /personas/gardener: status %d (%s)", w.Code, w.Body.String())
	}

	// A compaction is the moment the prompt used to be re-photographed.
	s.CompactAtTokens = 3000
	s.KeepVerbatimTokens = 500
	_ = a.store.PutSession(s)
	growWithToolResults(t, a, s, 6)
	if err := a.Compact(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	text, ok := promptSection(t, a, s, "persona")
	if !ok {
		t.Fatal("the session lost its persona section")
	}
	if strings.Contains(text, "DRAINAGE") {
		t.Errorf("an edited persona reached a running conversation:\n%s", text)
	}
	if !strings.Contains(text, "speak of soil") {
		t.Errorf("the persona in force is not the one the session was created with:\n%s", text)
	}
}

// The criterion: a session has exactly one prompt entry for its whole life, and
// a compaction writes none.
func TestACompactionWritesNoPromptEntry(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- folded")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	s.CompactAtTokens = 3000
	s.KeepVerbatimTokens = 500
	_ = a.store.PutSession(s)
	growWithToolResults(t, a, s, 6)

	countBefore := len(a.store.Entries(s.ID))
	if err := a.Compact(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	entries := a.store.Entries(s.ID)
	if len(entries) != countBefore+1 {
		t.Errorf("entry count %d → %d; a compaction appends exactly one entry, the compaction itself",
			countBefore, len(entries))
	}
	prompts := 0
	for _, e := range entries {
		if e.Type == "prompt" {
			prompts++
		}
	}
	if prompts != 1 {
		t.Errorf("session has %d prompt entries after compacting; want 1, the one it was created with", prompts)
	}
}

// The same for the skills index: a session lists the skills it had, described the
// way they were described when it started.
func TestEditingASkillDescriptionDoesNotReachARunningSession(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- kept going")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	if w := callAPI(t, a, "POST", "/skills", map[string]any{
		"name": "watering", "description": "When to water the tomatoes.",
		"body": "Twice a week, more in August.",
	}); w.Code != 200 {
		t.Fatalf("POST /skills: status %d (%s)", w.Code, w.Body.String())
	}

	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := promptSection(t, a, s, "skills_index"); !strings.Contains(text, "water the tomatoes") {
		t.Fatalf("the skill was not indexed in the session that followed it: %q", text)
	}

	if w := callAPI(t, a, "PUT", "/skills/watering", map[string]any{
		"description": "When to water the ORCHIDS.",
		"body":        "Twice a week, more in August.",
	}); w.Code != 200 {
		t.Fatalf("PUT /skills/watering: status %d (%s)", w.Code, w.Body.String())
	}

	s.CompactAtTokens = 3000
	s.KeepVerbatimTokens = 500
	_ = a.store.PutSession(s)
	growWithToolResults(t, a, s, 6)
	if err := a.Compact(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	text, _ := promptSection(t, a, s, "skills_index")
	if strings.Contains(text, "ORCHIDS") {
		t.Errorf("an edited skill description reached a running conversation:\n%s", text)
	}
}

// The other half: an edited persona does reach a conversation, by forking it.
// That is one cache write, in a session the operator chose to start.
func TestAForkTakesThePersonaAsItThenStands(t *testing.T) {
	a := newTestApp(t)
	if w := callAPI(t, a, "POST", "/personas", map[string]any{
		"name": "gardener", "description": "Tends the beds.",
		"body": "You are a gardener. You speak of soil.",
	}); w.Code != 200 {
		t.Fatalf("POST /personas: status %d (%s)", w.Code, w.Body.String())
	}
	s, err := a.NewSession(SessionConfig{Model: "test/model", Persona: "gardener"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := callAPI(t, a, "PUT", "/personas/gardener", map[string]any{
		"description": "Tends the beds.",
		"body":        "You are a gardener. You speak of DRAINAGE.",
	}); w.Code != 200 {
		t.Fatalf("PUT /personas/gardener: status %d (%s)", w.Code, w.Body.String())
	}

	fork, err := a.Fork(s, s.SessionConfig)
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := promptSection(t, a, fork, "persona"); !strings.Contains(text, "DRAINAGE") {
		t.Errorf("a fork did not take the persona as it now stands:\n%s", text)
	}
	// And the origin is untouched by its own fork.
	if text, _ := promptSection(t, a, s, "persona"); strings.Contains(text, "DRAINAGE") {
		t.Errorf("forking rewrote the origin's prompt:\n%s", text)
	}
}
