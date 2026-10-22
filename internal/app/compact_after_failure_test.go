package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A conversation compacts after a turn that failed, as after one that
// answered. Compaction ran only at the end of a turn that succeeded, so a turn
// that grew the conversation and then failed left all of it to be sent again,
// whole, with every call of the next turn.
func TestAConversationCompactsAfterATurnThatFailed(t *testing.T) {
	a := newTestApp(t)
	// Every turn fails; only the summary a compaction asks for is answered.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "Output the record and nothing else") {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"the request was refused"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"content": "## Decisions\n- broadband: fibre only"}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s := newSession(t, a)
	s.CompactAtTokens = 3000
	s.KeepVerbatimTokens = 500
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}
	growWithToolResults(t, a, s, 6)

	w := callAPI(t, a, "POST", "/sessions/"+s.ID+"/messages", map[string]any{"text": "And the prices?"})
	if w.Code != 202 {
		t.Fatalf("send = %d: %s", w.Code, w.Body.String())
	}
	settle(t, a, s.ID)

	failedAt, compactedAt := -1, -1
	for i, e := range a.store.Entries(s.ID) {
		if e.Type == "event" && e.EventKind == "error" && failedAt < 0 {
			failedAt = i
		}
		if e.Type == "compaction" {
			compactedAt = i
		}
	}
	if failedAt < 0 {
		t.Fatal("the turn did not fail, so this tests nothing")
	}
	if compactedAt < failedAt {
		t.Errorf("no compaction followed the failed turn (failure at %d, last compaction at %d)", failedAt, compactedAt)
	}
}
