package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The workspace boundary was a convention: read, write, and edit refused a path
// outside the session's directory, and bash — which is a shell and does what it
// is told — did not. One session's tools could read another's files and the
// operator's API key. The boundary has to be the operating system's, not the
// tool's good manners.
func TestTheSandboxNamesWhatIsEnforcing(t *testing.T) {
	s := NewSandbox(Config{Workspace: "/w", DataDir: "/d", EnvFile: "/e/.env"})
	switch runtime.GOOS {
	case "darwin":
		if s.Mechanism != "seatbelt" && s.Mechanism != "none" {
			t.Errorf("mechanism = %q, want seatbelt or none on darwin", s.Mechanism)
		}
	case "linux":
		if s.Mechanism != "bubblewrap" && s.Mechanism != "none" {
			t.Errorf("mechanism = %q, want bubblewrap or none on linux", s.Mechanism)
		}
	}
	// Whatever it decided, it has to be able to say why: a sandbox that quietly
	// does nothing is worse than none at all.
	if s.Mechanism == "none" && s.Reason == "" {
		t.Error("an unenforced sandbox must say why")
	}
}

// The wrapped command is what actually runs, so its shape is worth pinning:
// the tool binary and its arguments survive, and the confinement is in front.
func TestWrappingKeepsTheCommandIntact(t *testing.T) {
	for _, mech := range []string{"seatbelt", "bubblewrap", "none"} {
		t.Run(mech, func(t *testing.T) {
			s := &Sandbox{Mechanism: mech, workspaceRoot: "/w", dataDir: "/d", toolsDir: "/t", dbPath: "/d/agent.db"}
			argv := s.Wrap("/tools/bash/run", "/w/S1", "/t", []string{"-x"})
			if argv[len(argv)-1] != "-x" {
				t.Errorf("argv = %v, want it to end with the tool's own argument", argv)
			}
			if !contains(argv, "/tools/bash/run") {
				t.Errorf("argv = %v, want it to run the tool", argv)
			}
			if mech == "none" && argv[0] != "/tools/bash/run" {
				t.Errorf("argv = %v, want the bare command when nothing enforces", argv)
			}
			if mech != "none" && argv[0] == "/tools/bash/run" {
				t.Errorf("argv = %v, want the confinement in front of the command", argv)
			}
		})
	}
}

// bubblewrap binds only the runtime and the session's own directory, so a path
// nobody named is not in the mount namespace at all — the home directory and the
// repository the agent runs from included.
func TestBubblewrapBindsOnlyWhatIsAllowed(t *testing.T) {
	s := &Sandbox{Mechanism: "bubblewrap", workspaceRoot: "/w", dataDir: "/d",
		toolsDir: "/t", dbPath: "/d/agent.db"}
	argv := strings.Join(s.Wrap("/tools/bash/run", "/w/S1", "/t", nil), " ")
	if strings.Contains(argv, "--ro-bind / /") {
		t.Error("the whole filesystem is bound, which is the rule this replaces")
	}
	if !strings.Contains(argv, "--bind /w/S1 /w/S1") {
		t.Errorf("argv = %q, want this session's directory writable", argv)
	}
	if strings.Contains(argv, "--bind /w /w") || strings.Contains(argv, "--bind /d /d") {
		t.Errorf("argv = %q, want neither the workspace root nor the data directory writable", argv)
	}
	if !strings.Contains(argv, "--ro-bind /t /t") {
		t.Errorf("argv = %q, want the tool directory readable and not writable", argv)
	}
	if !strings.Contains(argv, "--die-with-parent") {
		t.Errorf("argv = %q, want the child to die with the agent", argv)
	}
}

// bwrap applies its arguments in order, and a tmpfs over /tmp will mask anything
// already mounted beneath it. A workspace under /tmp is ordinary — it is where
// Go's own temporary directories live on Linux, so it is what the test suite
// itself uses — and masking it leaves a tool unable to reach the one directory
// it is allowed to write.
func TestATmpfsOverTmpDoesNotMaskAWorkspaceBeneathIt(t *testing.T) {
	s := &Sandbox{Mechanism: "bubblewrap", workspaceRoot: "/tmp/w", dataDir: "/tmp/d",
		toolsDir: "/t", dbPath: "/tmp/d/agent.db"}
	argv := s.Wrap("/tools/bash/run", "/tmp/w/S1", "/t", nil)

	tmpfs, bind := -1, -1
	for i, a := range argv {
		if a == "--tmpfs" && i+1 < len(argv) && argv[i+1] == "/tmp" {
			tmpfs = i
		}
		if a == "--bind" && i+1 < len(argv) && argv[i+1] == "/tmp/w/S1" {
			bind = i
		}
	}
	if tmpfs < 0 || bind < 0 {
		t.Fatalf("argv = %v, want both a tmpfs over /tmp and a bind of the workspace", argv)
	}
	if tmpfs > bind {
		t.Errorf("the tmpfs over /tmp is applied after the workspace bind, which masks it: %v", argv)
	}
}

// The Seatbelt profile is an allow-list, and the property that makes it one is
// that no rule denies: a path nobody thought of is refused by the default, not
// permitted by an omission from a deny-list.
func TestTheSeatbeltProfileOnlyAllows(t *testing.T) {
	p := seatbeltProfile([]string{"/opt/browsers"})
	if !strings.HasPrefix(p, "(version 1)\n(deny default)") {
		t.Fatalf("the profile must deny by default:\n%s", p)
	}
	for _, line := range strings.Split(p, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "(deny ") &&
			strings.TrimSpace(line) != "(deny default)" {
			t.Errorf("the profile denies a specific path, so it is a deny-list again: %q", line)
		}
	}
	if !strings.Contains(p, `(allow file-read* file-write* (subpath (param "WS")))`) {
		t.Error("the session's own directory must be readable and writable")
	}
	if !strings.Contains(p, `(subpath "/opt/browsers")`) {
		t.Error("a path the operator named must reach the profile")
	}
	// The root directory is needed to resolve any path, and as a subpath it
	// would allow the whole filesystem — which is exactly the old behaviour.
	if strings.Contains(p, `(subpath "/")`) {
		t.Error("the root is allowed as a subpath, which permits everything")
	}
	if !strings.Contains(p, `(literal "/")`) {
		t.Error("the root must be readable as a literal, or no path resolves")
	}
	// A rule for the database alone leaves SQLite unable to open its journals.
	for _, param := range []string{"DB", "DBWAL", "DBSHM"} {
		if !strings.Contains(p, `(param "`+param+`")`) {
			t.Errorf("the profile does not name %s, so a tool cannot use its own tables", param)
		}
	}
}

// The whole point, driven through the real registry with the real bash tool:
// a shell in one session's directory must not reach another's. This is the test
// that would have caught the hole — bash read a sibling's file and the
// operator's API key, while read, write, and edit refused the same path.
func TestAShellInOneSessionCannotReachAnother(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(dir, "workspace")
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("OPENROUTER_API_KEY=sk-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: dir, Workspace: workspace, ToolsDir: "../../tools",
		DefaultModel: "test/model", EnvFile: envFile}
	sandbox := NewSandbox(cfg)
	a := &App{cfg: cfg, sandbox: sandbox, store: st,
		tools:  NewRegistry(cfg.ToolsDir, filepath.Join(dir, "agent.db"), st.DB()),
		skills: NewSkills(cfg.SkillsDir), hub: NewHub(),
		queues: map[string]chan func(){}, busy: map[string]bool{}}
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %v", failures)
	}

	victim, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	prowler, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(a.ensureWorkspace(victim.ID), "secret.txt")
	if err := os.WriteFile(secret, []byte("the other conversation's notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.ensureWorkspace(prowler.ID)

	run := func(command string) toolResult {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"command": command})
		return a.tools.Call(context.Background(),
			&ToolCtx{App: a, SessionID: prowler.ID}, "bash", args)
	}

	sibling := run("cat " + secret)
	key := run("cat " + envFile)
	own := run("echo mine > own.txt && cat own.txt")

	if !a.sandbox.Enforcing() {
		// No enforcement is a legitimate state on a machine without the
		// primitive — but it must be the state the system reports, not a
		// surprise. The reach is then expected, and says so.
		if !strings.Contains(a.sandbox.Describe(), "NOT ENFORCED") {
			t.Fatalf("nothing is enforcing but the system does not say so: %q", a.sandbox.Describe())
		}
		t.Logf("no sandbox on this machine (%s); the reach below is expected", a.sandbox.Reason)
		return
	}

	if sibling.OK {
		t.Errorf("a shell read another session's file: %q", sibling.Content)
	}
	if key.OK && strings.Contains(key.Content, "sk-secret") {
		t.Errorf("a shell read the API key: %q", key.Content)
	}
	// Confinement that also breaks the session's own directory would be useless.
	if !own.OK || !strings.Contains(own.Content, "mine") {
		t.Errorf("a shell could not use its own directory: ok=%v %s%s", own.OK, own.Content, own.Error)
	}
}

// copyToolTree duplicates a directory, executable bits and all — the registry
// refuses a tool whose run is not executable, so the mode is part of the copy.
// The test below
// needs a tool directory it can safely watch fail to be written, and using the
// repository's own would mean a machine with no sandbox — a legitimate state —
// silently editing the tools it is testing with.
func copyToolTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The specification led with an agent that wrote its own tools and skills, and
// the sandbox had quietly ended both: neither directory is writable. ADR-033
// removed the claim rather than the boundary, so the boundary is now deliberate
// and has to be pinned — a tool is refused by the operating system, not by its
// own good manners.
func TestAToolCannotModifyTheAgent(t *testing.T) {
	// Both directories are copies, in their own temporary directory rather than
	// the one handed to the app, so a machine with no sandbox — a legitimate
	// state — cannot edit the repository it is testing with.
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	skillsDir := filepath.Join(root, "skills")
	copyToolTree(t, "../../tools", toolsDir)
	copyToolTree(t, "../../skills", skillsDir)

	a, _ := confinedApp(t, toolsDir, skillsDir, "")
	run := shellIn(t, a)

	// A tool's executable is a compiled binary, so "did it change" is asked of
	// its bytes rather than of a word in it: the Go runtime already contains
	// most words anyone would grep for.
	before := digest(t, filepath.Join(toolsDir, "clock", "run"))

	installTool := run("mkdir -p " + toolsDir + "/self_installed")
	rewriteTool := run("echo broken >> " + toolsDir + "/clock/run")
	writeSkill := run("echo hi > " + skillsDir + "/self_written.md")
	readTool := run("cat " + toolsDir + "/clock/manifest.json")

	if !a.sandbox.Enforcing() {
		if !strings.Contains(a.sandbox.Describe(), "NOT ENFORCED") {
			t.Fatalf("nothing is enforcing but the system does not say so: %q", a.sandbox.Describe())
		}
		t.Logf("no sandbox on this machine (%s); the agent can modify itself, as reported", a.sandbox.Reason)
	} else {
		for _, c := range []struct {
			what string
			res  toolResult
			path string
		}{
			{"installed a tool", installTool, filepath.Join(toolsDir, "self_installed")},
			{"rewrote another tool", rewriteTool, ""},
			{"wrote a skill", writeSkill, filepath.Join(skillsDir, "self_written.md")},
		} {
			if c.res.OK {
				t.Errorf("a tool %s: %q", c.what, c.res.Content)
			}
			// A refusal reported but not enforced is the failure this exists
			// for, so the filesystem is asked rather than the tool.
			if c.path != "" {
				if _, err := os.Stat(c.path); err == nil {
					t.Errorf("%s: the path exists, so the refusal was not enforced", c.what)
				}
			}
		}
		if digest(t, filepath.Join(toolsDir, "clock", "run")) != before {
			t.Error("another tool's executable was modified despite the refusal")
		}
	}
	// A tool must still be able to read the directory it was loaded from, or it
	// could not have been executed in the first place.
	if !readTool.OK || !strings.Contains(readTool.Content, `"clock"`) {
		t.Errorf("a tool could not read the tool directory: ok=%v %s%s",
			readTool.OK, readTool.Content, readTool.Error)
	}
}

// digest is the content of a file, for asking whether it changed.
func digest(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// Reads were a deny-list: everything was readable but two named paths, so a
// tool could read the operator's private keys, their other repositories, and
// anything else on the disk. ADR-034 made reads an allow-list. This is the test
// that would have caught the hole, and it asks about paths that exist rather
// than ones invented for it.
func TestAToolReadsNothingOutsideItsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	copyToolTree(t, "../../tools", toolsDir)

	// Somewhere outside every allowed path, holding something worth taking.
	outside := t.TempDir()
	secret := filepath.Join(outside, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Nothing is named in AGENT_READ_PATHS: the allow-list is the runtime,
	// the tool directory, and this session — and that is all.
	a, envFile := confinedApp(t, toolsDir, "", "")
	run := shellIn(t, a)

	elsewhere := run("cat " + secret)
	key := run("cat " + envFile)
	home := run("ls " + mustHome(t))
	repo := run("ls " + mustAbs(t, "../.."))
	own := run("echo mine > own.txt && cat own.txt")
	runtime := run("python3 -c 'print(1 + 1)'")

	if !a.sandbox.Enforcing() {
		if !strings.Contains(a.sandbox.Describe(), "NOT ENFORCED") {
			t.Fatalf("nothing is enforcing but the system does not say so: %q", a.sandbox.Describe())
		}
		t.Logf("no sandbox on this machine (%s); the reach below is expected", a.sandbox.Reason)
		return
	}
	for _, c := range []struct {
		what string
		res  toolResult
	}{
		{"a file outside every allowed path", elsewhere},
		{"the file holding the API key", key},
		{"the operator's home directory", home},
		{"the repository the agent runs from", repo},
	} {
		if c.res.OK {
			t.Errorf("a tool read %s: %q", c.what, c.res.Content)
		}
	}
	if strings.Contains(elsewhere.Content+elsewhere.Error, "PRIVATE KEY") {
		t.Error("the contents came back inside the refusal")
	}
	// Confinement that also stopped a tool working would be useless.
	if !own.OK || !strings.Contains(own.Content, "mine") {
		t.Errorf("a tool could not use its own directory: ok=%v %s%s", own.OK, own.Content, own.Error)
	}
	if !runtime.OK || !strings.Contains(runtime.Content, "2") {
		t.Errorf("a tool could not run an interpreter: ok=%v %s%s",
			runtime.OK, runtime.Content, runtime.Error)
	}
}

// A path the operator names is readable and still not writable: AGENT_READ_PATHS
// adds to the allow-list and cannot take the boundary away.
func TestANamedReadPathIsReadableAndNotWritable(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	copyToolTree(t, "../../tools", toolsDir)

	named := t.TempDir()
	if err := os.WriteFile(filepath.Join(named, "browser.txt"), []byte("a browser lives here"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, _ := confinedApp(t, toolsDir, "", named)
	run := shellIn(t, a)
	read := run("cat " + filepath.Join(named, "browser.txt"))
	write := run("echo no > " + filepath.Join(named, "written.txt"))

	if !read.OK || !strings.Contains(read.Content, "a browser lives here") {
		t.Errorf("a named path was not readable: ok=%v %s%s", read.OK, read.Content, read.Error)
	}
	if !a.sandbox.Enforcing() {
		t.Logf("no sandbox on this machine (%s); the write below is expected", a.sandbox.Reason)
		return
	}
	if write.OK {
		t.Error("a named read path was writable, so the variable removes a boundary rather than adding one")
	}
	// The boundary in force has to be readable, not inferred, so it goes out
	// over the same surface the interface reads.
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/status", nil))
	if w.Code != 200 {
		t.Fatalf("/status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), absOr(named)) {
		t.Errorf("/status does not report the named read path: %s", w.Body.String())
	}
}

// confinedApp builds an app over the real registry with the real tools, and
// returns it with the path of the env file holding its API key.
func confinedApp(t *testing.T, toolsDir, skillsDir, readPaths string) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, "..", ".env")
	if err := os.WriteFile(envFile, []byte("OPENROUTER_API_KEY=sk-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: dir, Workspace: filepath.Join(dir, "workspace"),
		ToolsDir: toolsDir, SkillsDir: skillsDir, DefaultModel: "test/model",
		EnvFile: envFile, ReadPaths: readPaths}
	a := &App{cfg: cfg, sandbox: NewSandbox(cfg), store: st,
		tools:  NewRegistry(cfg.ToolsDir, filepath.Join(dir, "agent.db"), st.DB()),
		skills: NewSkills(cfg.SkillsDir), hub: NewHub(),
		queues: map[string]chan func(){}, busy: map[string]bool{}}
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %v", failures)
	}
	a.sched = NewScheduler(a)
	a.or = NewOpenRouter("")
	return a, envFile
}

// shellIn returns a function that runs a shell command through the real bash
// tool in a fresh session — a shell, because a shell does what it is told and
// so is the only honest way to ask what the boundary actually permits.
func shellIn(t *testing.T, a *App) func(string) toolResult {
	t.Helper()
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return func(command string) toolResult {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"command": command})
		return a.tools.Call(context.Background(),
			&ToolCtx{App: a, SessionID: s.ID}, "bash", args)
	}
}

func mustHome(t *testing.T) string {
	t.Helper()
	h, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// A tool is handed AGENT_DB deliberately and some of them write to it — notes
// keeps its items there. A sandbox that confined writes to the session's own
// directory broke that tool silently, and no test ran a tool that writes to the
// database, so nothing said so.
func TestASandboxedToolCanStillWriteToTheDatabase(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: dir, Workspace: filepath.Join(dir, "workspace"),
		ToolsDir: "../../tools", DefaultModel: "test/model"}
	sandbox := NewSandbox(cfg)
	a := &App{cfg: cfg, sandbox: sandbox, store: st,
		tools:  NewRegistry(cfg.ToolsDir, filepath.Join(dir, "agent.db"), st.DB()),
		skills: NewSkills(cfg.SkillsDir), hub: NewHub(),
		queues: map[string]chan func(){}, busy: map[string]bool{}}
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %v", failures)
	}
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]string{"action": "add", "text": "kept through the sandbox"})
	res := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "notes", args)
	if !res.OK {
		t.Fatalf("notes could not write: %s", res.Error)
	}

	list, _ := json.Marshal(map[string]string{"action": "list"})
	back := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "notes", list)
	if !back.OK || !strings.Contains(back.Content, "kept through the sandbox") {
		t.Errorf("the note did not survive: ok=%v %s%s", back.OK, back.Content, back.Error)
	}
}
