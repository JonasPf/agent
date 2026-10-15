package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// compactedConversation is a session whose first exchange a compaction folded
// and whose second it kept word for word.
func compactedConversation(t *testing.T, a *App) *Session {
	t.Helper()
	s := newSession(t, a)
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "What does the old sensor read?"})
	folded := a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "FOLDED-ANSWER: it reads 41 degrees"})
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "And the new one?"})
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "KEPT-ANSWER: 39 degrees"})
	a.append(s.ID, Entry{Type: "compaction", CoversThrough: folded.Seq,
		Text: "The old sensor reads 41 degrees.", FoldedTurns: 1})
	return s
}

// A fork carries a compacted conversation as the agent was reading it. The
// copy is renumbered behind the fork's own prompt and seeding event, so what
// the compaction covers has to be renumbered with it: left as it was, it
// covered one entry short, and the last folded entry came back into the
// fork's context, after the summary that already stood for it.
func TestAForkOfACompactedConversationSendsWhatTheOriginSends(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "noted")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	origin := compactedConversation(t, a)
	w := callAPI(t, a, "POST", "/sessions/"+origin.ID+"/fork", map[string]any{})
	if w.Code != 201 {
		t.Fatalf("fork: status %d (%s)", w.Code, w.Body.String())
	}
	var fork Session
	if err := json.Unmarshal(w.Body.Bytes(), &fork); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{origin.ID, fork.ID} {
		if w := callAPI(t, a, "POST", "/sessions/"+id+"/messages", map[string]any{"text": "Which is right?"}); w.Code != 202 {
			t.Fatalf("send to %s: status %d (%s)", id, w.Code, w.Body.String())
		}
		settle(t, a, id)
	}

	if len(*seen) < 2 {
		t.Fatalf("expected a call from each session, got %d", len(*seen))
	}
	texts := func(body string) []string {
		var req struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range req.Messages {
			if m["role"] == "system" {
				continue
			}
			c, _ := json.Marshal(m["content"])
			out = append(out, string(c))
		}
		return out
	}
	want, got := texts((*seen)[0]), texts((*seen)[1])
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the fork sent a different conversation from its origin\norigin: %q\nfork:   %q", want, got)
	}
	for _, m := range got {
		if strings.Contains(m, "FOLDED-ANSWER") {
			t.Errorf("the fork sent an entry its compaction covers: %s", m)
		}
	}
}
