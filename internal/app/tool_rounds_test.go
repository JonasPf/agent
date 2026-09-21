package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// toolCallingModel answers with a call to one tool for the first rounds, and
// with text once they are used up — a task that takes many steps before there
// is anything to say.
func toolCallingModel(t *testing.T, tool string, rounds int, final string) *httptest.Server {
	t.Helper()
	n := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Content-Type", "text/event-stream")
		var chunk []byte
		if n <= rounds {
			chunk, _ = json.Marshal(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": fmt.Sprintf("call_%d", n),
					"function": map[string]any{"name": tool, "arguments": "{}"},
				}}}}}})
		} else {
			chunk, _ = json.Marshal(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]any{"content": final}}}})
		}
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// A turn runs as many rounds of tools as the work takes. A bound of twelve
// stopped a research task in the middle and wrote nothing at all: the
// conversation simply went quiet, which is the one thing that may not happen.
func TestATurnRunsAsManyToolRoundsAsTheWorkTakes(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	writeTool(t, dir, "ticker", "#!/bin/sh\nprintf '{\"ok\":true,\"content\":\"tick\"}'\n")
	a.tools.dir = dir
	if _, f := a.tools.Load(a); len(f) > 0 {
		t.Fatalf("load failures: %v", f)
	}
	srv := toolCallingModel(t, "ticker", 16, "Here is the knowledge base.")
	t.Cleanup(srv.Close)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s := newSession(t, a)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/sessions/"+s.ID+"/messages",
		strings.NewReader(`{"text":"Build a knowledge base of every study you can find."}`))
	req.Header.Set("Content-Type", "application/json")
	a.routes().ServeHTTP(w, req)
	if w.Code != 202 {
		t.Fatalf("send status = %d: %s", w.Code, w.Body.String())
	}
	settle(t, a, s.ID)

	var calls, assistants int
	var last string
	for _, e := range a.store.Entries(s.ID) {
		if e.Type != "message" {
			continue
		}
		switch e.Role {
		case "assistant":
			assistants++
			if e.Text != "" {
				last = e.Text
			}
		case "tool":
			calls++
		}
	}
	if calls != 16 {
		t.Errorf("the turn ran %d tool calls, want the 16 the work took", calls)
	}
	if assistants != 17 {
		t.Errorf("the turn wrote %d assistant entries, want 17", assistants)
	}
	if !strings.Contains(last, "knowledge base") {
		t.Errorf("the turn ended on %q, want the model's answer", last)
	}
}
