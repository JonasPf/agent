package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The model writes an edit's old string from what it remembers of the file,
// and every miss in a month of use was a near one: a word or a space off. Each
// was followed by a read of the whole file to find out what it had got wrong.
// The refusal now quotes the line it nearly matched, so the retry needs no read.
func TestAnEditThatNearlyMatchesQuotesTheLineItMeant(t *testing.T) {
	a, s := userlandSession(t)
	ws := a.ensureWorkspace(s.ID)
	if err := os.MkdirAll(filepath.Join(ws, "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	csv := "date,meal,item\n2026-10-11,breakfast,gouda,on crispbread\n2026-10-11,lunch,rice,plain\n"
	if err := os.WriteFile(filepath.Join(ws, "log", "food.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]string{"path": "log/food.csv",
		"old": "2026-10-11,breakfast,gouda,on rye crispbread", "new": "2026-10-11,breakfast,gouda,on oatcake"})
	res := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "edit", args)
	if res.OK {
		t.Fatal("an edit that matches nothing was applied")
	}
	if !strings.Contains(res.Error, "line 2") || !strings.Contains(res.Error, "gouda,on crispbread") {
		t.Errorf("the refusal says %q; it must quote the line it nearly matched", res.Error)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "log", "food.csv")); string(b) != csv {
		t.Errorf("a refused edit changed the file: %q", b)
	}
}
