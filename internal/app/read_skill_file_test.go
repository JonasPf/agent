package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// "A path in the folder of a skill the conversation has is refused with the
// skill_read call that reads it." read reads the working directory and nothing
// else. In five voice-over conversations the agent read the skill's own script
// with it first, was told only that the path was outside the workspace, and
// found skill_read on its next call.
func TestReadingASkillsFileNamesTheCallThatReadsIt(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	a.cfg.ToolsDir = filepath.Join("..", "..", "tools")
	a.tools = NewRegistry(a.cfg.ToolsDir, DBPath(dir), a.store.DB())
	userSkills := t.TempDir()
	writeSkillFolder(t, userSkills, "trip", "", map[string]string{
		"packing.md":      "Chargers are one item per device.\n",
		"scripts/pack.py": "print('packed')\n",
	})
	writeSkillFolder(t, userSkills, "hidden", "default: off\n", map[string]string{
		"secret.md": "Not for this conversation.\n",
	})
	a.skills = NewSkills(filepath.Join("..", "..", "skills"), userSkills)
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %+v", failures)
	}
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	read := func(path string) toolResult {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"path": path})
		return a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "read", args)
	}

	res := read(filepath.Join(userSkills, "trip", "scripts", "pack.py"))
	if res.OK || !strings.Contains(res.Error, "outside the workspace") ||
		!strings.Contains(res.Error, `skill_read {"name": "trip", "file": "scripts/pack.py"}`) {
		t.Errorf("read of a skill's script = %+v, want a refusal naming the skill_read call that reads it", res)
	}
	// The call it names works.
	args, _ := json.Marshal(map[string]string{"name": "trip", "file": "scripts/pack.py"})
	if got := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "skill_read", args); !got.OK || got.Content != "print('packed')\n" {
		t.Errorf("the call the refusal names = %+v, want the script", got)
	}

	// A skill this conversation does not have, and anywhere else, are only outside.
	for _, path := range []string{filepath.Join(userSkills, "hidden", "secret.md"), "/etc/hostname"} {
		if res := read(path); res.OK || !strings.Contains(res.Error, "outside the workspace") || strings.Contains(res.Error, "skill_read") {
			t.Errorf("read %s = %+v, want only that it is outside the workspace", path, res)
		}
	}
}
