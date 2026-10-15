package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// A search is the words a person or the agent is looking for, each matching
// as a prefix. Punctuation and words the index would read as syntax are looked
// for like any other: a hyphen in "follow-up" used to be read as a column
// filter and answered with "no such column: up", a dot in "notes.md" and an
// unclosed quote as syntax errors, and "AND" on its own as one.
func TestASearchIsWordsWhateverTheyContain(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	a.append(s.ID, Entry{Type: "message", Role: "assistant",
		Text: `Booked the follow-up for Tuesday; details in notes.md. Ran ls -la at 3:2 and "quoted" AND it worked.`})

	for _, q := range []string{"follow-up", "notes.md", `"quoted`, "AND", "ls -la", "3:2", "Tuesday;", "(", "follow"} {
		w := callAPI(t, a, "GET", "/search?q="+url.QueryEscape(q)+"&session="+s.ID, nil)
		if w.Code != 200 {
			t.Errorf("search %q: status %d (%s)", q, w.Code, w.Body.String())
			continue
		}
		var hits []SearchHit
		if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil {
			t.Fatalf("search %q: %v (%s)", q, err, w.Body.String())
		}
		if q == "(" {
			if hits == nil || len(hits) != 0 {
				t.Errorf("search %q, which holds no word, answered %s; want an empty list", q, w.Body.String())
			}
			continue
		}
		if len(hits) != 1 {
			t.Errorf("search %q found %d entries, want the one that holds it", q, len(hits))
		}
	}
	if w := callAPI(t, a, "GET", "/search?q=chimney", nil); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("a search matching nothing answered %s, want []", w.Body.String())
	}
}

// The agent searches with the same words. The tool used to pass them on as
// the index's own query language, which its description asked the model to
// write, and a model writes "follow-up".
func TestTheSearchToolTakesWordsWithPunctuation(t *testing.T) {
	a, s := userlandSession(t)
	stop, err := a.listenTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "Booked the follow-up for Tuesday."})

	raw, _ := json.Marshal(map[string]any{"query": "follow-up"})
	res := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "session_search", raw)
	if !res.OK {
		t.Fatalf("session_search failed: %s%s", res.Content, res.Error)
	}
	if !strings.Contains(res.Content, "follow-up") || !strings.Contains(res.Content, s.ID) {
		t.Errorf("session_search did not find the entry:\n%s", res.Content)
	}
}
