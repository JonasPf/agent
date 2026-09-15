package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// The model a new conversation starts on is the one the operator last chose,
// not a setting in a file they have to edit and restart for. The most recent
// session is no substitute: a fork, an import, or a conversation the agent
// started can be the most recent and on a model nobody picked for new work.

func call(t *testing.T, a *App, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func decodeSession(t *testing.T, w *httptest.ResponseRecorder) Session {
	t.Helper()
	if w.Code >= 300 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var s Session
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return s
}

func preferredModel(t *testing.T, a *App) string {
	t.Helper()
	w := call(t, a, "GET", "/preferences", "")
	if w.Code != 200 {
		t.Fatalf("GET /preferences: %d %s", w.Code, w.Body.String())
	}
	var p struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return p.Model
}

func TestANewConversationStartsOnTheModelLastChosen(t *testing.T) {
	a := newTestApp(t)
	decodeSession(t, call(t, a, "POST", "/sessions", `{"model":"x/chosen"}`))

	// Something other than a choice becomes the most recent session.
	if _, err := a.NewSession(SessionConfig{Model: "y/other"}, ""); err != nil {
		t.Fatal(err)
	}

	if got := preferredModel(t, a); got != "x/chosen" {
		t.Errorf("preferred model = %q, want x/chosen", got)
	}
	if s := decodeSession(t, call(t, a, "POST", "/sessions", `{}`)); s.Model != "x/chosen" {
		t.Errorf("a conversation started without a model runs on %q, want the one last chosen", s.Model)
	}
}

func TestTheChosenModelOutlivesTheProcess(t *testing.T) {
	dir := t.TempDir()
	first := newTestAppAt(t, dir)
	decodeSession(t, call(t, first, "POST", "/sessions", `{"model":"x/chosen"}`))

	second := newTestAppAt(t, dir)
	if got := preferredModel(t, second); got != "x/chosen" {
		t.Errorf("after a restart the preferred model is %q, want x/chosen", got)
	}
}

// Forking onto another model is choosing it. Forking that keeps the model says
// nothing about what the next conversation should run on.
func TestForkingOntoAnotherModelIsAChoice(t *testing.T) {
	a := newTestApp(t)
	origin := decodeSession(t, call(t, a, "POST", "/sessions", `{"model":"x/chosen"}`))

	decodeSession(t, call(t, a, "POST", "/sessions/"+origin.ID+"/fork", `{"model":"z/better"}`))
	if got := preferredModel(t, a); got != "z/better" {
		t.Errorf("after forking onto z/better the preferred model is %q", got)
	}

	decodeSession(t, call(t, a, "POST", "/sessions/"+origin.ID+"/fork", `{"model":"x/chosen","enabled_tools":[]}`))
	decodeSession(t, call(t, a, "POST", "/sessions/"+origin.ID+"/fork", `{}`))
	if got := preferredModel(t, a); got != "z/better" {
		t.Errorf("a fork that kept its origin's model changed the preference to %q", got)
	}
}

// An agent that ran before choices were remembered has sessions and no choice.
// Its next conversation continues on what it was using, rather than jumping to
// a fallback nobody picked.
func TestWithNoChoiceYetTheMostRecentConversationsModelIsKept(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.NewSession(SessionConfig{Model: "y/in-use"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := preferredModel(t, a); got != "y/in-use" {
		t.Errorf("preferred model = %q, want the most recent conversation's y/in-use", got)
	}
}

// Before anything has been chosen there is still a model to start on, and it is
// named rather than empty: a session with no model is one no call can be made
// for.
func TestBeforeAnyChoiceAConversationStillHasAModel(t *testing.T) {
	a := newTestApp(t)
	if got := preferredModel(t, a); got != fallbackModel {
		t.Errorf("preferred model with nothing chosen = %q, want %q", got, fallbackModel)
	}
	s := decodeSession(t, call(t, a, "POST", "/sessions", `{}`))
	if s.Model == "" || s.Model != fallbackModel {
		t.Errorf("first conversation runs on %q, want %q", s.Model, fallbackModel)
	}
}
