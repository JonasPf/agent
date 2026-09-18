package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// notes is scoped the way session_search is: the calling conversation unless it
// asks to widen (ADR-055). Before that it listed every conversation's notes, which
// made it a second store crossing the session boundary without anyone asking.
//
// End to end: the real tool, run as a subprocess through the registry, against
// the real database.
func TestNotesAreScopedToTheCallingConversation(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	a.cfg.ToolsDir = filepath.Join("..", "..", "tools")
	a.tools = NewRegistry(a.cfg.ToolsDir, DBPath(dir), a.store.DB())
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %+v", failures)
	}
	toolAPI(t, a)

	mine, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	theirs, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")

	call := func(sessionID, args string) toolResult {
		t.Helper()
		res := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: sessionID}, "notes",
			json.RawMessage(args))
		if !res.OK {
			t.Fatalf("notes %s failed: %s (stderr %s)", args, res.Error, res.stderr)
		}
		return res
	}

	call(mine.ID, `{"action":"add","text":"the kiln fires at 1240"}`)
	call(theirs.ID, `{"action":"add","text":"the invoice is overdue"}`)

	// Unscoped: this conversation's notes, and no others.
	got := call(mine.ID, `{"action":"list"}`).Content
	if !strings.Contains(got, "kiln") {
		t.Errorf("an unscoped listing lost this conversation's own note:\n%s", got)
	}
	if strings.Contains(got, "invoice") {
		t.Errorf("an unscoped listing returned another conversation's note:\n%s", got)
	}

	// Widened deliberately: every conversation's.
	all := call(mine.ID, `{"action":"list","session":"all"}`).Content
	if !strings.Contains(all, "kiln") || !strings.Contains(all, "invoice") {
		t.Errorf(`session:"all" did not return every conversation's notes:\n%s`, all)
	}

	// Named: that conversation's.
	named := call(mine.ID, `{"action":"list","session":"`+theirs.ID+`"}`).Content
	if !strings.Contains(named, "invoice") || strings.Contains(named, "kiln") {
		t.Errorf("naming a session did not return that session's notes:\n%s", named)
	}

	// A conversation with nothing of its own is told how to widen, rather than
	// being left to read "no notes" as "nothing was ever noted anywhere".
	empty, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	none := call(empty.ID, `{"action":"list"}`).Content
	if !strings.Contains(none, "all") {
		t.Errorf("an empty listing does not say how to widen the scope:\n%s", none)
	}
}
