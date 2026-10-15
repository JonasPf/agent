package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// growTurns writes n whole turns, each named so the test can tell which of them
// reached the model: a user message, a tool call, a modest result, an answer.
func growTurns(t *testing.T, a *App, s *Session, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		a.append(s.ID, Entry{Type: "message", Role: "user", Text: fmt.Sprintf("question-%02d", i)})
		a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "Looking.",
			ToolCalls: []ToolCall{{ID: "c", Name: "web_browse", Arguments: `{"url":"https://x"}`}}})
		body, _ := json.Marshal(map[string]any{"ok": true, "content": strings.Repeat("page text ", 60)})
		a.append(s.ID, Entry{Type: "message", Role: "tool", ToolCallID: "c",
			ToolName: "web_browse", ToolResult: body})
		a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: fmt.Sprintf("answer-%02d", i)})
	}
}

// What the model was sent on its newest call, as one string.
func lastRequest(t *testing.T, seen *[]string) string {
	t.Helper()
	if len(*seen) == 0 {
		t.Fatal("the model was never called")
	}
	return (*seen)[len(*seen)-1]
}

// The criterion behind keep_verbatim_tokens: the newest turns survive a
// compaction word for word. The compaction is written after them, so a
// projection that only sends what follows the compaction entry sends the
// summary and drops the very turns it was meant to keep.
func TestTheVerbatimTailReachesTheModelAfterACompaction(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "## Decisions\n- SUMMARY-OF-THE-OLD")
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
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: "what now?"}); err != nil {
		t.Fatal(err)
	}
	req := lastRequest(t, seen)

	summary := strings.Index(req, "SUMMARY-OF-THE-OLD")
	if summary < 0 {
		t.Fatal("the summary did not reach the model")
	}
	if strings.Contains(req, "question-01") {
		t.Fatal("a turn the compaction covers still reaches the model")
	}
	// The newest turns were kept verbatim, and come after the summary they
	// continue from.
	for _, kept := range []string{"question-12", "answer-12", "question-11"} {
		at := strings.Index(req, kept)
		if at < 0 {
			t.Fatalf("%s was carried over verbatim but never reached the model", kept)
		}
		if at < summary {
			t.Fatalf("%s reached the model ahead of the summary of what came before it", kept)
		}
	}
	if strings.Index(req, "what now?") < strings.Index(req, "answer-12") {
		t.Fatal("the new message was not sent after the turns carried over")
	}
}

// An edit appends a compaction covering the same range as the one it corrects.
// What the first one carried over must still be carried over.
func TestAnEditedSummaryKeepsTheTurnsCarriedOver(t *testing.T) {
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

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("PUT", "/sessions/"+s.ID+"/compaction",
		strings.NewReader(`{"text":"## Decisions\n- EDITED-SUMMARY"}`)))
	if w.Code != 200 {
		t.Fatalf("PUT /compaction = %d: %s", w.Code, w.Body)
	}
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: "go on"}); err != nil {
		t.Fatal(err)
	}
	req := lastRequest(t, seen)
	if strings.Contains(req, "FIRST-SUMMARY") || !strings.Contains(req, "EDITED-SUMMARY") {
		t.Fatal("the edit did not replace the summary sent")
	}
	if !strings.Contains(req, "question-12") {
		t.Fatal("editing the summary dropped the turns it carried over")
	}
}

func sessionOver(t *testing.T, a *App, id string) Session {
	t.Helper()
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+id, nil))
	if w.Code != 200 {
		t.Fatalf("GET /sessions/%s = %d: %s", id, w.Code, w.Body)
	}
	var out struct {
		Session Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Session
}

// The threshold is the session's own, defaults to forty thousand, and can be
// changed at any point in the conversation without touching any other.
func TestTheCompactionThresholdIsSetPerSession(t *testing.T) {
	a := newTestApp(t)
	one, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	two, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if got := sessionOver(t, a, one.ID); got.CompactAtTokens != 40000 || got.KeepVerbatimTokens != 10000 {
		t.Fatalf("a new session compacts at %d keeping %d; want 40000 and 10000",
			got.CompactAtTokens, got.KeepVerbatimTokens)
	}

	if w := patchSession(t, a, one.ID, `{"compact_at_tokens":80000,"keep_verbatim_tokens":16000}`); w.Code != 200 {
		t.Fatalf("PATCH thresholds = %d: %s", w.Code, w.Body)
	}
	got := sessionOver(t, a, one.ID)
	if got.CompactAtTokens != 80000 || got.KeepVerbatimTokens != 16000 {
		t.Fatalf("after PATCH: compacts at %d keeping %d; want 80000 and 16000",
			got.CompactAtTokens, got.KeepVerbatimTokens)
	}
	if other := sessionOver(t, a, two.ID); other.CompactAtTokens != 40000 {
		t.Fatalf("changing one session's threshold moved another's to %d", other.CompactAtTokens)
	}

	// One without the other leaves the other alone.
	if w := patchSession(t, a, one.ID, `{"compact_at_tokens":60000}`); w.Code != 200 {
		t.Fatalf("PATCH compact_at_tokens alone = %d: %s", w.Code, w.Body)
	}
	if got := sessionOver(t, a, one.ID); got.CompactAtTokens != 60000 || got.KeepVerbatimTokens != 16000 {
		t.Fatalf("after a threshold-only PATCH: %d keeping %d; want 60000 keeping 16000",
			got.CompactAtTokens, got.KeepVerbatimTokens)
	}
	// And a rename leaves both alone.
	if w := patchSession(t, a, one.ID, `{"title":"renamed"}`); w.Code != 200 {
		t.Fatalf("rename = %d: %s", w.Code, w.Body)
	}
	if got := sessionOver(t, a, one.ID); got.CompactAtTokens != 60000 {
		t.Fatalf("a rename moved the threshold to %d", got.CompactAtTokens)
	}
}

// A pair that would compact constantly, or never, is refused with the reason,
// and leaves the session as it was.
func TestAThresholdThatCannotWorkIsRefused(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	for _, body := range []string{
		`{"compact_at_tokens":1000}`,                               // below the floor
		`{"compact_at_tokens":16000}`,                              // the default tail is more than half of it
		`{"compact_at_tokens":40000,"keep_verbatim_tokens":30000}`, // tail more than half
		`{"keep_verbatim_tokens":-1}`,
	} {
		w := patchSession(t, a, s.ID, body)
		if w.Code != 400 {
			t.Errorf("PATCH %s = %d; want 400", body, w.Code)
			continue
		}
		if !strings.Contains(w.Body.String(), "tokens") {
			t.Errorf("PATCH %s refused without saying why: %s", body, w.Body)
		}
	}
	if got := sessionOver(t, a, s.ID); got.CompactAtTokens != 40000 || got.KeepVerbatimTokens != 10000 {
		t.Fatalf("a refused PATCH changed the session: %d keeping %d", got.CompactAtTokens, got.KeepVerbatimTokens)
	}
}

// A threshold set over HTTP is the one the next turn compacts at.
func TestALoweredThresholdCompactsAfterTheNextTurn(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- lowered")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	growTurns(t, a, s, 30)
	if used := a.contextUsed(s.ID, a.store.Entries(s.ID)); used < 4000 || used >= 40000 {
		t.Fatalf("setup holds %d tokens; want between the lowered threshold and the default", used)
	}
	if w := patchSession(t, a, s.ID, `{"compact_at_tokens":4000,"keep_verbatim_tokens":1000}`); w.Code != 200 {
		t.Fatalf("PATCH = %d: %s", w.Code, w.Body)
	}
	if err := a.runTurn(context.Background(), a.store.Session(s.ID), turnOpts{UserText: "and?"}); err != nil {
		t.Fatal(err)
	}
	if newestCompaction(a.store.Entries(s.ID)) < 0 {
		t.Fatal("a session over its lowered threshold did not compact after the turn")
	}
}

// The threshold can change for the whole life of the session, and the prompt is
// written once. A number in the prompt would go stale the first time it moved.
func TestThePromptDoesNotStateTheThreshold(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	p, ok := a.promptEntry(s.ID)
	if !ok {
		t.Fatal("no prompt entry")
	}
	text := systemMessage(p.Sections)
	if !strings.Contains(text, "compacts") {
		t.Fatal("the prompt no longer tells the model that the session compacts")
	}
	for _, n := range []string{strconv.Itoa(s.CompactAtTokens), "40,000"} {
		if strings.Contains(text, n) {
			t.Fatalf("the prompt states the threshold %s, which can change after it is written", n)
		}
	}
}

// A fork copies the conversation, and how the conversation is kept is part of it.
func TestAForkKeepsItsOriginsThresholds(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if w := patchSession(t, a, s.ID, `{"compact_at_tokens":90000,"keep_verbatim_tokens":20000}`); w.Code != 200 {
		t.Fatalf("PATCH = %d: %s", w.Code, w.Body)
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/fork", strings.NewReader(`{}`)))
	if w.Code != 201 {
		t.Fatalf("fork = %d: %s", w.Code, w.Body)
	}
	var fork Session
	_ = json.Unmarshal(w.Body.Bytes(), &fork)
	if fork.CompactAtTokens != 90000 || fork.KeepVerbatimTokens != 20000 {
		t.Fatalf("the fork compacts at %d keeping %d; want its origin's 90000 and 20000",
			fork.CompactAtTokens, fork.KeepVerbatimTokens)
	}
}

// Compaction happens only between turns, and a compaction asked for is no
// exception: it waits for the turn in progress rather than folding under it.
func TestARequestedCompactionWaitsForTheTurnInProgress(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- waited")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	s.CompactAtTokens = 1 << 20
	s.KeepVerbatimTokens = 1200
	_ = a.store.PutSession(s)
	growTurns(t, a, s, 12)

	release := make(chan struct{})
	a.enqueue(s.ID, func(context.Context) { <-release }) // a turn in progress

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/compact", nil))
		done <- w
	}()

	select {
	case w := <-done:
		t.Fatalf("the compaction answered (%d) while a turn was still running", w.Code)
	case <-time.After(300 * time.Millisecond):
	}
	if newestCompaction(a.store.Entries(s.ID)) >= 0 {
		t.Fatal("the session compacted while a turn was still running")
	}

	close(release)
	select {
	case w := <-done:
		if w.Code != 200 {
			t.Fatalf("POST /compact = %d: %s", w.Code, w.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the compaction never ran after the turn ended")
	}
	if newestCompaction(a.store.Entries(s.ID)) < 0 {
		t.Fatal("the requested compaction wrote nothing once the turn ended")
	}
}

// A conversation whose whole length fits the verbatim tail has nothing to fold.
// Asked to compact, it says so, rather than answering as if it had.
func TestARequestedCompactionWithNothingToFoldSaysSo(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "## Decisions\n- unused")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	growTurns(t, a, s, 2)

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/compact", nil))
	if w.Code != 409 {
		t.Fatalf("POST /compact on a short conversation = %d; want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "nothing to compact") {
		t.Fatalf("the refusal does not say why: %s", w.Body)
	}
	if len(*seen) != 0 {
		t.Fatal("a compaction with nothing to fold still called the model")
	}
	if newestCompaction(a.store.Entries(s.ID)) >= 0 {
		t.Fatal("a compaction with nothing to fold wrote an entry")
	}
}

// A person searching needs to know who said a thing, when, and whether the
// agent can still see it — the last is what decides whether it has to be
// told again.
func TestASearchHitSaysWhoWhenAndWhetherItWasCompacted(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	old := a.append(s.ID, Entry{Type: "message", Role: "user", Text: "the boiler pressure is low"})
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "boiler noted"})
	a.append(s.ID, Entry{Type: "compaction", CoversThrough: old.Seq, Text: "## Facts\n- low pressure"})
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "the boiler is fixed"})

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/search?q=boiler&session="+s.ID, nil))
	if w.Code != 200 {
		t.Fatalf("GET /search = %d: %s", w.Code, w.Body)
	}
	var hits []struct {
		Seq       int       `json:"seq"`
		Role      string    `json:"role"`
		CreatedAt time.Time `json:"created_at"`
		Compacted bool      `json:"compacted"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("got %d hits; want 3: %s", len(hits), w.Body)
	}
	for _, h := range hits {
		if h.CreatedAt.IsZero() {
			t.Errorf("hit %d carries no time", h.Seq)
		}
		wantRole := "assistant"
		if h.Seq == old.Seq {
			wantRole = "user"
		}
		if h.Role != wantRole {
			t.Errorf("hit %d: role %q, want %q", h.Seq, h.Role, wantRole)
		}
		if want := h.Seq <= old.Seq; h.Compacted != want {
			t.Errorf("hit %d: compacted = %v, want %v", h.Seq, h.Compacted, want)
		}
	}
}
