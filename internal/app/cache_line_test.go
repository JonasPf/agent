package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// usageModel answers every call with "noted" and the usage a provider reports:
// how many tokens were sent and how many of them were read from its cache.
func usageModel(t *testing.T, prompt, cached int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": "noted"}}},
			"usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": 3, "cost": 0.001,
				"prompt_tokens_details": map[string]any{"cached_tokens": cached}},
		})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

type cacheView struct {
	Session struct {
		Cache *struct {
			Tokens    int       `json:"tokens"`
			ExpiresAt time.Time `json:"expires_at"`
			KeptBy    string    `json:"kept_by"`
		} `json:"cache"`
	} `json:"session"`
}

func sessionCache(t *testing.T, a *App, id string) cacheView {
	t.Helper()
	var v cacheView
	if err := json.Unmarshal(callAPI(t, a, "GET", "/sessions/"+id, nil).Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func say(t *testing.T, a *App, id, text string) {
	t.Helper()
	if w := callAPI(t, a, "POST", "/sessions/"+id+"/messages", map[string]any{"text": text}); w.Code != 202 {
		t.Fatalf("send: status %d (%s)", w.Code, w.Body.String())
	}
	settle(t, a, id)
}

// The status line counts down to the live cache's expiry, so the agent says
// what it holds: how large the prefix it last sent is, and, where the agent
// wrote the marker itself and so knows the lifetime it asked for, when that
// prefix expires. A provider that caches on its own reports no lifetime, and
// none is invented for it.
func TestASessionSaysWhatItsCacheHoldsAndWhenItExpires(t *testing.T) {
	a := newTestApp(t)
	a.or = NewOpenRouter("test-key")
	a.or.base = usageModel(t, 6716, 6000).URL

	s, err := a.NewSession(SessionConfig{Model: "anthropic/claude-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if c := sessionCache(t, a, s.ID).Session.Cache; c != nil {
		t.Fatalf("a session that has made no call reports a cache: %+v", c)
	}
	// An operator who has just written is talking, so the turn writes a
	// marker; what the session holds is what its last call sent.
	say(t, a, s.ID, "First.")
	say(t, a, s.ID, "Second.")
	c := sessionCache(t, a, s.ID).Session.Cache
	if c == nil {
		t.Fatal("a call that wrote a cache marker left the session reporting no cache")
	}
	if c.Tokens != 6716 || c.KeptBy != "agent" {
		t.Errorf("cache = %+v, want 6716 tokens kept by the agent's marker", *c)
	}
	if left := time.Until(c.ExpiresAt); left < 4*time.Minute || left > 5*time.Minute+5*time.Second {
		t.Errorf("the cache expires in %v, want the five minutes the marker asked for", left)
	}

	b := newTestApp(t)
	b.or = NewOpenRouter("test-key")
	b.or.base = usageModel(t, 3000, 2500).URL
	o, err := b.NewSession(SessionConfig{Model: "openai/gpt-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	say(t, b, o.ID, "Hello.")
	pc := sessionCache(t, b, o.ID).Session.Cache
	if pc == nil || pc.KeptBy != "provider" || pc.Tokens != 3000 || !pc.ExpiresAt.IsZero() {
		t.Errorf("a provider that caches on its own: cache = %+v, want 3000 tokens kept by the provider, no expiry", pc)
	}
}

// A compaction rewrites the head of what is sent, so whatever was cached is
// of no further use, and the session stops claiming it.
func TestACompactionResetsTheCache(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- asked for")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	s, _ := a.NewSession(SessionConfig{Model: "anthropic/claude-test"}, "")
	s.CompactAtTokens = 1 << 20
	s.KeepVerbatimTokens = 500
	s.Cache = &SessionCache{Tokens: 9000, ExpiresAt: time.Now().Add(4 * time.Minute), KeptBy: "agent"}
	_ = a.store.PutSession(s)
	growWithToolResults(t, a, s, 6)

	if w := callAPI(t, a, "POST", "/sessions/"+s.ID+"/compact", nil); w.Code != 200 {
		t.Fatalf("POST /compact = %d: %s", w.Code, w.Body)
	}
	if c := sessionCache(t, a, s.ID).Session.Cache; c != nil {
		t.Errorf("after a compaction the session still reports a cache: %+v", c)
	}
}

// The operator who has just sent a message is talking now, so the turn it
// starts is cached like any other: its tool rounds follow it seconds apart and
// read what the first round wrote. The gate used to look for a recent message
// before this one was stored, so the first message of a conversation, and the
// first after five quiet minutes, wrote no marker and every round of that turn
// paid full price.
func TestTheFirstMessageAfterAPauseIsCached(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "noted")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	s, err := a.NewSession(SessionConfig{Model: "anthropic/claude-test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// Whether any call a message made carried the marker. A first message is
	// followed by a call that titles the conversation, which caches nothing.
	marked := func(text string) bool {
		before := len(*seen)
		say(t, a, s.ID, text)
		for _, body := range (*seen)[before:] {
			if strings.Contains(body, "cache_control") {
				return true
			}
		}
		return false
	}
	if !marked("The very first message.") {
		t.Error("the first message of a conversation wrote no cache marker")
	}

	// Ten quiet minutes, then the operator is back.
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "Earlier.", CreatedAt: time.Now().Add(-10 * time.Minute)})
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "Long ago.", CreatedAt: time.Now().Add(-10 * time.Minute)})
	if !marked("I'm back.") {
		t.Error("the first message after a pause wrote no cache marker")
	}
}
