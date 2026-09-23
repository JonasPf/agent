package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// namedModel answers with the identifier it was asked of, so a test can tell
// one candidate's reply from another's.
func namedModel(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Model string `json:"model"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &in)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"content": "answered by " + in.Model}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// comparisonApp is an agent whose model server names itself in every reply, and
// one titled session to compare from. A title already set keeps the candidates
// from spending a call on naming themselves.
func comparisonApp(t *testing.T) (*App, *Session) {
	t.Helper()
	a := newTestApp(t)
	srv := namedModel(t)
	t.Cleanup(srv.Close)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	s := newSession(t, a)
	s.Title = "Greenhouse sensors"
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}
	return a, s
}

func compareVia(t *testing.T, a *App, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/sessions/"+id+"/compare", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	return w
}

// candidates reads the sessions a comparison created, in the order it made them.
func candidates(t *testing.T, w *httptest.ResponseRecorder) (string, []*Session) {
	t.Helper()
	var out struct {
		Comparison string     `json:"comparison"`
		Candidates []*Session `json:"candidates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("comparison response: %v (%s)", err, w.Body.String())
	}
	return out.Comparison, out.Candidates
}

func lastAssistant(a *App, sessionID string) string {
	entries := a.store.Entries(sessionID)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "message" && entries[i].Role == "assistant" {
			return entries[i].Text
		}
	}
	return ""
}

// The whole point of a comparison: one question, several models, each answering
// from the same conversation. A candidate is a fork, so it carries the history
// and a working directory of its own, and it belongs to the comparison until
// the comparison is decided.
func TestAComparisonPutsOneMessageToEveryModel(t *testing.T) {
	a, s := comparisonApp(t)
	w := compareVia(t, a, s.ID,
		`{"text":"Which sensor should I replace?","models":["a/one","b/two","c/three"]}`)
	if w.Code != 201 {
		t.Fatalf("compare = %d: %s", w.Code, w.Body.String())
	}
	group, cands := candidates(t, w)
	if group == "" {
		t.Fatal("a comparison came back without an identifier")
	}
	if len(cands) != 3 {
		t.Fatalf("three models chosen, %d candidates created", len(cands))
	}
	var models []string
	for _, c := range cands {
		models = append(models, c.Model)
		if c.ForkedFrom != s.ID {
			t.Errorf("candidate %s forked from %q, want the origin", c.ID, c.ForkedFrom)
		}
		if c.Comparison != group {
			t.Errorf("candidate %s names comparison %q, want %q", c.ID, c.Comparison, group)
		}
		settle(t, a, c.ID)
		if got := lastAssistant(a, c.ID); got != "answered by "+c.Model {
			t.Errorf("candidate on %s answered %q", c.Model, got)
		}
		asked := 0
		for _, e := range a.store.Entries(c.ID) {
			if e.Type == "message" && e.Role == "user" && e.Text == "Which sensor should I replace?" {
				asked++
			}
		}
		if asked != 1 {
			t.Errorf("candidate on %s was asked the question %d times, want once", c.Model, asked)
		}
	}
	if strings.Join(models, ",") != "a/one,b/two,c/three" {
		t.Errorf("candidates run on %v, want one per model chosen", models)
	}
}

// Comparing one model with nothing is not a comparison, and the refusal says so
// rather than quietly forking once.
func TestAComparisonNeedsTwoModels(t *testing.T) {
	a, s := comparisonApp(t)
	for _, body := range []string{
		`{"text":"Which one?","models":["a/one"]}`,
		`{"text":"Which one?","models":[]}`,
	} {
		w := compareVia(t, a, s.ID, body)
		if w.Code != 400 {
			t.Errorf("compare %s = %d, want 400", body, w.Code)
		}
	}
	w := compareVia(t, a, s.ID, `{"text":"  ","models":["a/one","b/two"]}`)
	if w.Code != 400 {
		t.Errorf("compare with no message = %d, want 400", w.Code)
	}
	for _, other := range a.store.Sessions() {
		if other.ID != s.ID {
			t.Fatalf("a refused comparison still created session %s", other.ID)
		}
	}
}

// The comparison happens beside the conversation, not in it. The origin is left
// exactly where it was, and says what was asked and who was asked it.
func TestTheOriginRecordsTheComparisonAndIsNotSentIt(t *testing.T) {
	a, s := comparisonApp(t)
	before := len(a.store.Entries(s.ID))
	w := compareVia(t, a, s.ID, `{"text":"Which sensor should I replace?","models":["a/one","b/two"]}`)
	if w.Code != 201 {
		t.Fatalf("compare = %d: %s", w.Code, w.Body.String())
	}
	group, cands := candidates(t, w)
	for _, c := range cands {
		settle(t, a, c.ID)
	}
	settle(t, a, s.ID)

	var compare []Entry
	for _, e := range a.store.Entries(s.ID)[before:] {
		if e.Type == "message" {
			t.Fatalf("the origin was sent the compared message: %s %q", e.Role, e.Text)
		}
		if e.EventKind == "compare" {
			compare = append(compare, e)
		}
	}
	if len(compare) != 1 {
		t.Fatalf("the origin holds %d compare events, want exactly one", len(compare))
	}
	text := compare[0].Text
	if !strings.Contains(text, "Which sensor should I replace?") {
		t.Errorf("the compare event does not say what was asked: %q", text)
	}
	for _, c := range cands {
		if !strings.Contains(text, c.ID) || !strings.Contains(text, c.Model) {
			t.Errorf("the compare event names neither %s nor %s: %q", c.ID, c.Model, text)
		}
	}
	if !strings.Contains(text, group) {
		t.Errorf("the compare event does not name the comparison %s: %q", group, text)
	}
}

// Candidates are separate sessions, and separate sessions have always run at the
// same time. A model that takes a minute must not hold the others behind it.
func TestCandidatesRunConcurrently(t *testing.T) {
	a := newTestApp(t)
	release := make(chan struct{})
	srv := slowModel(t, release)
	t.Cleanup(srv.Close)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	s := newSession(t, a)
	s.Title = "Greenhouse sensors"
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}

	w := compareVia(t, a, s.ID, `{"text":"Take your time.","models":["a/one","b/two","c/three"]}`)
	if w.Code != 201 {
		t.Fatalf("compare = %d: %s", w.Code, w.Body.String())
	}
	_, cands := candidates(t, w)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		busy := 0
		for _, c := range cands {
			if a.Busy(c.ID) {
				busy++
			}
		}
		if busy == len(cands) {
			close(release)
			for _, c := range cands {
				settle(t, a, c.ID)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	t.Fatal("the candidates never worked at the same time")
}

// Choosing is keeping: the winner carries on as the conversation, and the
// others go entirely — transcript, working directory, and row in the list.
// Both ends record which won.
func TestKeepingACandidateDeletesTheOthers(t *testing.T) {
	a, s := comparisonApp(t)
	w := compareVia(t, a, s.ID, `{"text":"Which sensor should I replace?","models":["a/one","b/two","c/three"]}`)
	if w.Code != 201 {
		t.Fatalf("compare = %d: %s", w.Code, w.Body.String())
	}
	_, cands := candidates(t, w)
	for _, c := range cands {
		settle(t, a, c.ID)
		if err := os.WriteFile(filepath.Join(a.sessionWorkspace(c.ID), "answer.txt"),
			[]byte(c.Model), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	winner, losers := cands[1], []*Session{cands[0], cands[2]}

	kw := httptest.NewRecorder()
	a.routes().ServeHTTP(kw, httptest.NewRequest("POST", "/sessions/"+winner.ID+"/keep", nil))
	if kw.Code != 200 {
		t.Fatalf("keep = %d: %s", kw.Code, kw.Body.String())
	}

	for _, l := range losers {
		if a.store.Session(l.ID) != nil {
			t.Errorf("candidate %s survived the choice", l.ID)
		}
		if _, err := os.Stat(a.sessionWorkspace(l.ID)); !os.IsNotExist(err) {
			t.Errorf("candidate %s kept its working directory", l.ID)
		}
	}
	kept := a.store.Session(winner.ID)
	if kept == nil {
		t.Fatal("the kept candidate was deleted")
	}
	if kept.Comparison != "" {
		t.Errorf("the kept candidate still names a comparison: %q", kept.Comparison)
	}
	if kept.Title != "Greenhouse sensors" {
		t.Errorf("the kept candidate is titled %q, want the conversation's own title", kept.Title)
	}
	if got := lastAssistant(a, winner.ID); got != "answered by b/two" {
		t.Errorf("the kept candidate holds %q", got)
	}
	if _, err := os.Stat(filepath.Join(a.sessionWorkspace(winner.ID), "answer.txt")); err != nil {
		t.Errorf("the kept candidate lost its working directory: %v", err)
	}

	var keptSaid, originSaid string
	for _, e := range a.store.Entries(winner.ID) {
		if e.EventKind == "kept" {
			keptSaid = e.Text
		}
	}
	for _, e := range a.store.Entries(s.ID) {
		if e.EventKind == "kept" {
			originSaid = e.Text
		}
	}
	if !strings.Contains(keptSaid, "b/two") {
		t.Errorf("the kept session does not say it was kept: %q", keptSaid)
	}
	if !strings.Contains(originSaid, winner.ID) {
		t.Errorf("the origin does not name the kept candidate: %q", originSaid)
	}
	for _, l := range losers {
		if !strings.Contains(originSaid, l.ID) {
			t.Errorf("the origin does not say %s was deleted: %q", l.ID, originSaid)
		}
	}
}

// A comparison is decided by reading, and a candidate can be read before the
// slowest has finished. One still working is stopped rather than deleted from
// under a running turn.
func TestKeepingStopsACandidateStillWorking(t *testing.T) {
	a := newTestApp(t)
	release := make(chan struct{})
	slow := slowModel(t, release)
	t.Cleanup(slow.Close)
	t.Cleanup(func() {
		defer func() { recover() }()
		close(release)
	})
	a.or = NewOpenRouter("test-key")
	a.or.base = slow.URL
	s := newSession(t, a)
	s.Title = "Greenhouse sensors"
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}

	w := compareVia(t, a, s.ID, `{"text":"Take your time.","models":["a/one","b/two"]}`)
	if w.Code != 201 {
		t.Fatalf("compare = %d: %s", w.Code, w.Body.String())
	}
	_, cands := candidates(t, w)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !(a.Busy(cands[0].ID) && a.Busy(cands[1].ID)) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.Busy(cands[1].ID) {
		t.Fatal("the candidate to be discarded never started working")
	}

	kw := httptest.NewRecorder()
	a.routes().ServeHTTP(kw, httptest.NewRequest("POST", "/sessions/"+cands[0].ID+"/keep", nil))
	if kw.Code != 200 {
		t.Fatalf("keep = %d: %s", kw.Code, kw.Body.String())
	}
	if a.store.Session(cands[1].ID) != nil {
		t.Fatal("the working candidate survived the choice")
	}
	if a.Busy(cands[1].ID) {
		t.Error("the discarded candidate was deleted with its turn still running")
	}
	// The kept candidate is still talking to a model that answers when it is
	// told to. Ended here so nothing is writing into the transcript once the
	// test has finished with it.
	a.StopTurn(cands[0].ID)
	settle(t, a, cands[0].ID)
}

// Keeping is how a comparison ends, so it means nothing anywhere else. An
// ordinary conversation asked to be kept is told what keeping is for.
func TestKeepingASessionThatIsNotACandidateIsRefused(t *testing.T) {
	a, s := comparisonApp(t)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/keep", nil))
	if w.Code != 409 {
		t.Fatalf("keep of an ordinary session = %d, want 409 (%s)", w.Code, w.Body.String())
	}

	cw := compareVia(t, a, s.ID, `{"text":"Which one?","models":["a/one","b/two"]}`)
	_, cands := candidates(t, cw)
	for _, c := range cands {
		settle(t, a, c.ID)
	}
	first := httptest.NewRecorder()
	a.routes().ServeHTTP(first, httptest.NewRequest("POST", "/sessions/"+cands[0].ID+"/keep", nil))
	if first.Code != 200 {
		t.Fatalf("keep = %d: %s", first.Code, first.Body.String())
	}
	again := httptest.NewRecorder()
	a.routes().ServeHTTP(again, httptest.NewRequest("POST", "/sessions/"+cands[0].ID+"/keep", nil))
	if again.Code != 409 {
		t.Errorf("keeping a decided comparison again = %d, want 409", again.Code)
	}
}
