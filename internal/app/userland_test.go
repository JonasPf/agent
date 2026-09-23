package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// userlandSession is an app over the real tool directory, and one session in
// it. Both tests below run shell commands through the bash tool as shipped,
// which is the only way to find out what the shell can actually do: a program
// that exists but cannot read its own library under the sandbox is the same as
// one that is not installed.
func userlandSession(t *testing.T) (*App, *Session) {
	t.Helper()
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
	return a, s
}

func runShell(t *testing.T, a *App, s *Session, command string) toolResult {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"command": command})
	return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "bash", args)
}

// The shell is more useful with the programs people reach for first: curl and
// wget to fetch, python3 and perl to compute, gh and glab to work on a
// repository. The image ships them, and they have to run under the sandbox, not
// merely exist — an interpreter that cannot read its own library is the same as
// one that is absent, and a forge client that cannot read its own configuration
// directory is the reason its credential arrives in the environment instead.
//
// The second half of the list is what a conversation about documents reaches
// for. Without it the model rebuilds it by hand: a session handed a folder of
// scanned medical records spent some thirty calls bootstrapping pip into a
// temporary directory, downloading a wheel, and resolving tesseract's shared
// libraries one .deb at a time before it could read the first page.
func TestTheShellRunsTheProgramsTheImageShips(t *testing.T) {
	a, s := userlandSession(t)

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
		// pdftotext, because a PDF is how a laboratory, a bank, and a public
		// body all deliver a document.
		{"pdftotext", "pdftotext -v 2>&1", "pdftotext"},
		// tesseract is asked for its languages rather than its version: the
		// binary without the language data reads nothing, and the two arrive as
		// separate packages.
		{"tesseract", "tesseract --list-langs 2>&1", "eng"},
		{"tesseract-deu", "tesseract --list-langs 2>&1", "deu"},
		// pip itself, so the long tail is one command rather than an afternoon.
		{"pip", "python3 -m pip --version", "pip"},
		{"numpy", "python3 -c 'import numpy; print(numpy.zeros(3).sum())'", "0.0"},
		{"pillow", "python3 -c 'import PIL; print(\"pillow\", PIL.__version__)'", "pillow"},
		{"jq", `echo '{"ok":1}' | jq -r .ok`, "1"},
		{"unzip", "unzip -v", "UnZip"},
	} {
		res := runShell(t, a, s, c.command)
		if res.OK && strings.Contains(res.Content, c.want) {
			continue
		}
		if os.Getenv("AGENT_TEST_FULL") == "" {
			notRun(t, "%s is not installed on this machine; the image and the test container ship it", c.program)
		}
		t.Errorf("%s did not run in the shell: ok=%v %s%s", c.program, res.OK, res.Content, res.Error)
	}
}

// A tool used to be handed the home directory of the user the container runs
// as, which Landlock grants no tool either read or write. Every program that
// keeps state there — pip, npm, git, glab — failed on a path it never
// mentioned, which reads as the program being broken rather than as a refusal.
// HOME is now a directory inside the session's own working directory: writable,
// confined by the same boundary as everything else the session writes, and not
// shared with any other conversation.
func TestAToolsHomeIsWritableAndInsideItsOwnSession(t *testing.T) {
	a, s := userlandSession(t)
	ws := a.ensureWorkspace(s.ID)

	res := runShell(t, a, s, "printf ok > $HOME/marker && echo HOME=$HOME")
	if !res.OK {
		if !a.sandbox.Enforcing() && os.Getenv("AGENT_TEST_FULL") == "" {
			notRun(t, "the sandbox is not enforced here, so HOME is not confined")
		}
		t.Fatalf("a tool could not write to its own home directory: %s%s", res.Content, res.Error)
	}
	if !strings.Contains(res.Content, "HOME="+ws) {
		t.Errorf("HOME is not inside the session's working directory: %q, want a path under %q", res.Content, ws)
	}
	if b, err := os.ReadFile(filepath.Join(ws, ".home", "marker")); err != nil || string(b) != "ok" {
		t.Errorf("what the tool wrote to HOME is not in the session's directory: %v %q", err, b)
	}
}

// What pip installs with --user has to land somewhere writable, or the model
// discovers the boundary the long way: by reading a permission error about a
// path under a home directory it was never told about.
func TestPipInstallsIntoTheSessionsOwnDirectory(t *testing.T) {
	a, s := userlandSession(t)
	ws := a.ensureWorkspace(s.ID)

	res := runShell(t, a, s, "python3 -m site --user-base")
	if !res.OK {
		if os.Getenv("AGENT_TEST_FULL") == "" {
			notRun(t, "python3 is not installed on this machine; the image and the test container ship it")
		}
		t.Fatalf("python3 could not say where --user installs: %s%s", res.Content, res.Error)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Content), ws) {
		t.Errorf("pip --user would install to %q, which is outside the session's directory %q",
			strings.TrimSpace(res.Content), ws)
	}
}
