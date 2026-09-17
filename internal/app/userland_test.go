package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shell is more useful with the programs people reach for first: curl and
// wget to fetch, python3 and perl to compute, gh and glab to work on a
// repository. The image ships them, and they have to run under the sandbox, not
// merely exist — an interpreter that cannot read its own library is the same as
// one that is absent, and a forge client that cannot read its own configuration
// directory is the reason its credential arrives in the environment instead.
func TestTheShellRunsTheProgramsTheImageShips(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: dir, Workspace: filepath.Join(dir, "workspace"), ToolsDir: "../../tools"}
	a := &App{cfg: cfg, sandbox: NewSandbox(cfg), store: st,
		tools:  NewRegistry(cfg.ToolsDir, DBPath(dir), st.DB()),
		skills: NewSkills(cfg.SkillsDir, cfg.UserSkillsDir), hub: NewHub(),
		queues: map[string]chan func(){}}
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %v", failures)
	}
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ program, command, want string }{
		{"curl", "curl --version", "curl"},
		{"wget", "wget --version", "Wget"},
		{"python3", "python3 -c 'import json; print(json.dumps({\"ok\": 1}))'", `{"ok": 1}`},
		{"perl", "perl -e 'print 6*7'", "42"},
		{"gh", "gh --version", "gh version"},
		// glab creates its configuration directory before it runs any command at
		// all, and its default is under a home directory no tool can write. It is
		// pointed at the session's own directory, which is writable; the skill
		// says the same thing in prose, because every glab call needs it.
		{"glab", "GLAB_CONFIG_DIR=$PWD/.glab glab --version", "glab"},
	} {
		args, _ := json.Marshal(map[string]string{"command": c.command})
		res := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "bash", args)
		if res.OK && strings.Contains(res.Content, c.want) {
			continue
		}
		if os.Getenv("AGENT_TEST_FULL") == "" {
			notRun(t, "%s is not installed on this machine; the image and the test container ship it", c.program)
		}
		t.Errorf("%s did not run in the shell: ok=%v %s%s", c.program, res.OK, res.Content, res.Error)
	}
}
