package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func searchTool(t *testing.T, a *App, sessionID string, args map[string]any) toolResult {
	t.Helper()
	raw, _ := json.Marshal(args)
	return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: sessionID}, "session_search", raw)
}

// A search hit is a dozen words around the match. The agent lost its own last
// answer to a compaction, was told "yes, I'll start that on Monday", and spent
// four hours and 696 searches assembling that answer back out of snippets. A
// hit's number now reads the entry it names, whole.
func TestASearchHitCanBeReadWhole(t *testing.T) {
	a, s := userlandSession(t)
	stop, err := a.listenTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	long := "Mostly yes, same dose and same slot, but five real differences. " +
		strings.Repeat("The form, the acidity, the price, and the tablet size all differ. ", 20) +
		"One process point: do not start the new capsule today."
	said := a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: long})

	res := searchTool(t, a, s.ID, map[string]any{"seq": said.Seq})
	if !res.OK {
		t.Fatalf("reading entry %d failed: %s%s", said.Seq, res.Content, res.Error)
	}
	if !strings.Contains(res.Content, "do not start the new capsule today") ||
		!strings.Contains(res.Content, "Mostly yes, same dose") {
		t.Errorf("the entry did not come back whole:\n%s", res.Content)
	}

	// Another conversation's entry is read only when it is asked for by name,
	// as a search of it is.
	other := newSession(t, a)
	there := a.append(other.ID, Entry{Type: "message", Role: "user", Text: "only in the other conversation"})
	if res := searchTool(t, a, s.ID, map[string]any{"seq": there.Seq, "session": other.ID}); !res.OK ||
		!strings.Contains(res.Content, "only in the other conversation") {
		t.Errorf("reading another conversation's entry by its id: ok=%v %s%s", res.OK, res.Content, res.Error)
	}

	res = searchTool(t, a, s.ID, map[string]any{"seq": 9999})
	if res.OK || !strings.Contains(res.Error, "9999") {
		t.Errorf("an entry that does not exist: ok=%v %s%s; want a failure naming it", res.OK, res.Content, res.Error)
	}
	if res := searchTool(t, a, s.ID, map[string]any{}); res.OK {
		t.Error("a call with neither words nor a number succeeded")
	}
}
