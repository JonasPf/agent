package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// A tool-heavy session grows almost entirely in tool results: the model's own
// words are a sentence between fetches, while the fetched pages are tens of
// thousands of tokens. Rotation counts those tokens, so such a session rotates
// early; if summarisation does not count them too, it never runs, and the
// successor is seeded from a predecessor that never wrote down what happened.
func TestASessionGrowingOnlyInToolResultsIsSummarised(t *testing.T) {
	a := newTestApp(t)
	a.cfg.SummaryEvery = 4000
	srv, _ := recordingModel(t, "They discussed broadband in Kinvara.")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	s.RotateAtTokens = 1 << 20 // this test is about summarising, not rotating
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "Find broadband options."})
	for i := 0; i < 4; i++ {
		a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "Fetching.",
			ToolCalls: []ToolCall{{ID: "c", Name: "web_fetch", Arguments: `{"url":"https://x"}`}}})
		body, _ := json.Marshal(map[string]any{"ok": true, "content": strings.Repeat("page text ", 1000)})
		a.append(s.ID, Entry{Type: "message", Role: "tool", ToolCallID: "c",
			ToolName: "web_fetch", ToolResult: body})
	}

	if grown := projectedTokens(a.store.Entries(s.ID)); grown < a.cfg.SummaryEvery {
		t.Fatalf("test setup grew only %d tokens, need at least %d", grown, a.cfg.SummaryEvery)
	}

	// Driven through the real turn, which is what summarises: the threshold is
	// only ever tested where the agent actually tests it.
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: "Anything else?"}); err != nil {
		t.Fatal(err)
	}

	// Read back over HTTP, the surface the operator reads it on.
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+s.ID, nil))
	if w.Code != 200 {
		t.Fatalf("GET /sessions/%s = %d", s.ID, w.Code)
	}
	var got struct {
		Session Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Session.Summary == "" {
		t.Fatal("a session that grew past the threshold in tool results was not summarised")
	}
}

// The predecessor's summary is context about the situation, not something the
// operator said. Sending it as a user message put words in the operator's mouth
// — the one slot that means "you are being addressed now" — so it travels in
// the system prompt instead, frozen for the successor's life.
func TestTheCarriedSummaryReachesTheModelAsSystemContext(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "Understood.")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	pred, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	pred.Summary = "They compared broadband providers for Kinvara."
	if err := a.store.PutSession(pred); err != nil {
		t.Fatal(err)
	}
	a.append(pred.ID, Entry{Type: "message", Role: "user", Text: "What is SIRO?"})

	succ, err := a.Rotate(pred, pred.SessionConfig, true, "size")
	if err != nil {
		t.Fatal(err)
	}
	if succ.CarriedSummary != pred.Summary {
		t.Fatalf("CarriedSummary = %q, want %q", succ.CarriedSummary, pred.Summary)
	}

	for _, e := range a.store.Entries(succ.ID) {
		if e.Type == "message" && strings.Contains(e.Text, pred.Summary) {
			t.Errorf("the summary was seeded as a %s message; it belongs in the prompt", e.Role)
		}
	}
	// The transcript still records it, in an event the projection drops, so the
	// record of what the successor started from survives without costing tokens.
	var recorded bool
	for _, e := range a.store.Entries(succ.ID) {
		if e.EventKind == "carried_over" && strings.Contains(e.Text, pred.Summary) {
			recorded = true
		}
	}
	if !recorded {
		t.Error("the carried_over event does not record the summary the successor started from")
	}

	if err := a.runTurn(context.Background(), succ, turnOpts{UserText: "Go on."}); err != nil {
		t.Fatal(err)
	}
	if len(*seen) == 0 {
		t.Fatal("the model was never called")
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte((*seen)[0]), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		t.Fatalf("first message was not the system prompt: %s", (*seen)[0])
	}
	if !strings.Contains(req.Messages[0].Content, pred.Summary) {
		t.Errorf("the system prompt did not carry the summary; it was %q", req.Messages[0].Content)
	}
}
