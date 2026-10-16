package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// routedModel answers each call as the gateway does when it routes a model to
// one of its providers: every chunk names the provider serving it. Which
// provider serves a call is chosen by the message it answers, not by the order
// calls arrive in: the session's title is asked for by a call of its own,
// alongside the first turn.
func routedModel(t *testing.T, providers map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content any `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		p := "Elsewhere"
		if n := len(req.Messages); n > 0 {
			if text, ok := req.Messages[n-1].Content.(string); ok && providers[text] != "" {
				p = providers[text]
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range []map[string]any{
			{"provider": p, "choices": []any{map[string]any{"delta": map[string]any{"content": "Served by "}}}},
			{"provider": p, "choices": []any{map[string]any{"delta": map[string]any{"content": p + "."}}},
				"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 4, "cost": 0.001}},
		} {
			b, _ := json.Marshal(c)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// One model is served by many providers, and the gateway chooses among them
// call by call. A provider whose deployment is broken answers in garbage while
// the rest answer well, and nothing else in the transcript tells the two
// apart, so each answer records which provider produced it.
func TestEachAnswerRecordsTheProviderThatServedIt(t *testing.T) {
	a := newTestApp(t)
	a.or = NewOpenRouter("test-key")
	a.or.base = routedModel(t, map[string]string{"First.": "Wafer", "Second.": "Decart"}).URL
	s, err := a.NewSession(SessionConfig{Model: "z-ai/glm-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	say(t, a, s.ID, "First.")
	say(t, a, s.ID, "Second.")

	w := callAPI(t, a, "GET", "/sessions/"+s.ID+"/transcript", nil)
	var entries []struct {
		Role  string `json:"role"`
		Text  string `json:"text"`
		Usage *struct {
			Provider string `json:"provider"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("transcript: %v (%s)", err, w.Body.String())
	}
	var got []string
	for _, e := range entries {
		if e.Role != "assistant" {
			continue
		}
		if e.Usage == nil {
			t.Fatalf("answer %q carries no usage", e.Text)
		}
		got = append(got, e.Usage.Provider)
	}
	if len(got) != 2 || got[0] != "Wafer" || got[1] != "Decart" {
		t.Errorf("answers name providers %q, want [Wafer Decart]", got)
	}
}
