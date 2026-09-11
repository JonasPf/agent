package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// newTestApp builds an App backed by temporary directories. It makes no model
// calls, so no API key is needed.
func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: dir, Workspace: dir, DefaultModel: "test/model"}
	a := &App{
		sandbox: NewSandbox(cfg),
		cfg:     cfg,
		store:   st,
		tools:   NewRegistry(filepath.Join(dir, "tools"), DBPath(dir), st.DB()),
		skills:  NewSkills(filepath.Join(dir, "skills")),
		hub:     NewHub(),
		queues:  map[string]chan func(){},
		busy:    map[string]bool{},
	}
	a.registerBuiltins()
	return a
}

func patchSession(t *testing.T, a *App, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PATCH", "/sessions/"+id, strings.NewReader(body))
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	return w
}

// A session's configuration settles at its first turn: until the prompt entry
// is written the session has told the model nothing, so it is still editable.
// A session's configuration is chosen before the session exists. Once it exists
// the prompt is already written, so there is no window in which a session is
// live and its configuration is still open — one state instead of two.
func TestPromptEntryExistsFromCreation(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "old/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	e, ok := a.promptEntry(s.ID)
	if !ok {
		t.Fatal("a session must have a prompt entry from the moment it exists")
	}
	if len(e.Sections) == 0 {
		t.Fatal("the prompt entry must carry its sections")
	}
	if e.Seq != 0 {
		t.Errorf("the prompt entry is position zero, got seq %d", e.Seq)
	}
}

func TestConfigCannotBeChangedOnAnExistingSession(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "old/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"model":"newer/model"}`,
		`{"enabled_tools":["memory"]}`,
		`{"enabled_skills":[]}`,
	} {
		w := patchSession(t, a, s.ID, body)
		if w.Code != http.StatusConflict {
			t.Fatalf("PATCH %s: want 409, got %d: %s", body, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "rotate") {
			t.Errorf("PATCH %s: the refusal must name rotation as the way: %s", body, w.Body.String())
		}
	}
	got := a.store.Session(s.ID)
	if got.Model != "old/model" || got.EnabledTools != nil || got.EnabledSkills != nil {
		t.Fatalf("a refused change must not be applied: %+v", got.SessionConfig)
	}
}

// Running turns must not add a second prompt: the prompt a session is sent on
// its last turn is the prompt it was sent on its first.
func TestSessionHasExactlyOnePromptEntry(t *testing.T) {
	a, _ := modelBackedApp(t, "first", "second")
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := a.promptEntry(s.ID)
	for _, text := range []string{"one", "two"} {
		if err := a.runTurn(context.Background(), s, turnOpts{UserText: text}); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, e := range a.store.Entries(s.ID) {
		if e.Type == "prompt" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one prompt entry, got %d", n)
	}
	again, _ := a.promptEntry(s.ID)
	if again.Seq != first.Seq {
		t.Fatalf("prompt entry changed: %d then %d", first.Seq, again.Seq)
	}
}

// A fork is created under a configuration chosen on the screen that creates it,
// so rotate carries the caller's configuration rather than inheriting silently.
func TestRotateCreatesTheSuccessorUnderTheGivenConfig(t *testing.T) {
	a := newTestApp(t)
	pred, err := a.NewSession(SessionConfig{Model: "old/model", EnabledTools: []string{"read"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	succ, err := a.Rotate(pred, SessionConfig{Model: "new/model", EnabledTools: []string{"bash"}}, false, "fork")
	if err != nil {
		t.Fatal(err)
	}
	if succ.Model != "new/model" {
		t.Errorf("successor model = %q, want new/model", succ.Model)
	}
	if _, ok := a.promptEntry(succ.ID); !ok {
		t.Error("a successor must have its prompt entry from creation too")
	}
	if a.store.Session(pred.ID).Model != "old/model" {
		t.Error("the predecessor's configuration must be untouched")
	}
}

// A tool registered after a session's prompt is written is not in that prompt, so
// it takes effect in the next session rather than this one.
func TestToolRegisteredAfterSettlingIsNotInThePrompt(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "old/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := a.promptEntry(s.ID)

	a.tools.mu.Lock()
	a.tools.register(&Tool{Name: "late_tool", Description: "registered after the first turn",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}}, Builtin: true,
		run: func(context.Context, *ToolCtx, json.RawMessage) (any, error) { return "", nil }})
	a.tools.mu.Unlock()

	after, _ := a.promptEntry(s.ID)
	if sectionsText(after) != sectionsText(before) {
		t.Fatal("an existing session's prompt must not change when a tool is registered")
	}
	if strings.Contains(sectionsText(after), "late_tool") {
		t.Fatal("a tool registered later must not appear in this session's prompt")
	}
	if !strings.Contains(promptTextFor(a, &Session{ID: "next", SessionConfig: SessionConfig{Model: "m"}}), "late_tool") {
		t.Fatal("a tool registered later must appear in the next session's prompt")
	}
}

func sectionsText(e Entry) string {
	var b strings.Builder
	for _, sec := range e.Sections {
		b.WriteString(sec.Name)
		b.WriteString(sec.Text)
	}
	return b.String()
}

func promptTextFor(a *App, s *Session) string {
	return sectionsText(Entry{Sections: a.systemSections(s)})
}

// Fields that never appear in the prompt stay editable for the session's life.
func TestMutableFieldsSurviveSettling(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "old/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	w := patchSession(t, a, s.ID, `{"title":"renamed"}`)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := a.store.Session(s.ID); got.Title != "renamed" {
		t.Fatalf("title not applied: %+v", got)
	}
}
