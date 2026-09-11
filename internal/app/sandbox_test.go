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
	if s.Mechanism != "landlock" && s.Mechanism != "none" {
		t.Errorf("mechanism = %q, want landlock or none — there is no second mechanism", s.Mechanism)
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
	for _, mech := range []string{"landlock", "none"} {
		t.Run(mech, func(t *testing.T) {
			s := &Sandbox{Mechanism: mech, workspaceRoot: "/w", dataDir: "/d", toolsDir: "/t", dbPath: "/d/db/agent.db"}
			argv := s.Wrap("/tools/bash/run", "/w/S1", "/t", nil, []string{"-x"})
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

// The Landlock policy is what the wrapper carries, so what it grants is worth
// pinning: the session's own directory and nothing above it, the runtime and the
// tool directory readable, and the database by name. A path nobody granted is
// denied, which is what makes this an allow-list.
func TestTheLandlockPolicyGrantsOnlyWhatIsAllowed(t *testing.T) {
	s := &Sandbox{Mechanism: "landlock", workspaceRoot: "/w", dataDir: "/d",
		toolsDir: "/t", dbPath: "/d/db/agent.db", ReadPaths: []string{"/opt/browsers"}}
	argv := s.Wrap("/tools/bash/run", "/w/S1", "/t", nil, []string{"-c", "true"})

	if len(argv) < 4 || argv[1] != "-confine" || argv[3] != "--" {
		t.Fatalf("argv = %v, want the agent's own wrapper in front", argv)
	}
	if argv[len(argv)-1] != "true" || argv[len(argv)-3] != "/tools/bash/run" {
		t.Errorf("argv = %v, want the command and its arguments intact at the end", argv)
	}

	var p policy
	if err := json.Unmarshal([]byte(argv[2]), &p); err != nil {
		t.Fatalf("policy is not readable: %v", err)
	}
	// Two writable trees and no more: this session's own directory, and the
	// directory holding the database a tool is handed on purpose. Not the data
	// directory above it, which holds every transcript.
	if len(p.Write) != 2 || p.Write[0] != "/w/S1" || p.Write[1] != "/d/db" {
		t.Errorf("writable = %v, want this session's directory and the database's own", p.Write)
	}
	for _, denied := range []string{"/w", "/d", "/"} {
		for _, granted := range append(append([]string{}, p.Write...), p.Read...) {
			if granted == denied {
				t.Errorf("%q is granted; the workspace root, the data directory and the root are not", denied)
			}
		}
	}
	readable := strings.Join(p.Read, " ")
	if !strings.Contains(readable, "/t") {
		t.Errorf("read = %v, want the tool directory readable", p.Read)
	}
	if !strings.Contains(readable, "/opt/browsers") {
		t.Errorf("read = %v, want the operator's named path readable", p.Read)
	}
	if p.Chdir != "/w/S1" {
		t.Errorf("chdir = %q, want the session's directory", p.Chdir)
	}
	// A program opens these before any of its own code runs, and granting them
	// one by one is what keeps the grant off the rest of /dev.
	if len(p.Files) == 0 || !strings.Contains(strings.Join(p.Files, " "), "/dev/null") {
		t.Errorf("files = %v, want the device files a program needs to start", p.Files)
	}
}

// The whole point, driven through the real registry with the real bash tool:
// a shell in one session's directory must not reach another's. This is the test
// that would have caught the hole — bash read a sibling's file and the
// operator's API key, while read, write, and edit refused the same path.
// confinementOrSkip refuses to let a test about the boundary pass on a machine
// that has no boundary. The agent is a Linux program; a laptop can run the suite
// but cannot confine a tool, and a test that quietly succeeded there would be
// reporting on nothing. CI and the container run these for real.
func confinementOrSkip(t *testing.T, s *Sandbox) {
	t.Helper()
	if !s.Enforcing() {
		t.Skipf("NOT RUN: nothing confines a tool here (%s). This test is proved on Linux — "+
			"in CI, and in the container with `task dev`.", s.Reason)
	}
}

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
		tools:  NewRegistry(cfg.ToolsDir, DBPath(dir), st.DB()),
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
		// Nothing enforcing is a legitimate state, and it must be the state the
		// system reports rather than a surprise — so that much is checked
		// everywhere. What cannot be checked here is the reach itself.
		if !strings.Contains(a.sandbox.Describe(), "NOT ENFORCED") {
			t.Fatalf("nothing is enforcing but the system does not say so: %q", a.sandbox.Describe())
		}
		confinementOrSkip(t, a.sandbox)
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
	confinementOrSkip(t, a.sandbox)
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
	confinementOrSkip(t, a.sandbox)
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
	confinementOrSkip(t, a.sandbox)
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
		tools:  NewRegistry(cfg.ToolsDir, DBPath(dir), st.DB()),
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
		tools:  NewRegistry(cfg.ToolsDir, DBPath(dir), st.DB()),
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

// A sandbox that is selected but cannot run confines nothing, and says it does.
// The deployed container had bubblewrap installed on a host that refuses
// unprivileged user namespaces: the agent reported "sandbox: bubblewrap", the
// interface showed a boundary, and every tool subprocess died on launch.
// Whether the mechanism is present was never the question — whether the kernel
// will run it is.
func TestAMechanismIsOnlyClaimedIfItActuallyRuns(t *testing.T) {
	ok, reason := landlockAvailable()
	if !ok && reason == "" {
		t.Error("a refusal must say why; an unexplained one is unactionable")
	}
	if ok && runtime.GOOS != "linux" {
		t.Errorf("Landlock reported available on %s", runtime.GOOS)
	}
}

// The invariant the whole boundary rests on: if the agent says it is confining
// tools, a tool has to actually run under that confinement. This is what the
// deployment violated — it claimed bubblewrap and could not launch anything.
func TestWhatTheSandboxClaimsIsWhatTheToolGets(t *testing.T) {
	a := newTestApp(t)
	installEnvTool(t, a, "canary", nil)
	if !a.sandbox.Enforcing() {
		t.Skipf("no sandbox on this machine (%s); the claim and the reality agree", a.sandbox.Reason)
	}
	// Enforcing, so the tool must run — under confinement, not despite it.
	seen := toolEnv(t, a, "canary")
	if !seen["PATH"] {
		t.Errorf("the sandbox claims %q but no tool can run under it", a.sandbox.Mechanism)
	}
}

// /proc is the one tree where a read grant is also a leak: it exposes the
// environment of every process this user owns, the agent's included. A browser
// needs it and nothing else does, so it is not in the runtime every tool gets —
// a tool that needs it asks, and the answer is written in its manifest.
func TestOnlyAToolThatAsksForAPathCanReadIt(t *testing.T) {
	s := &Sandbox{Mechanism: "landlock", workspaceRoot: "/w", dataDir: "/d",
		toolsDir: "/t", dbPath: "/d/db/agent.db"}

	plain := landlockPolicy(t, s.Wrap("/tools/edit/run", "/w/S1", "/t", nil, nil))
	for _, path := range plain.Read {
		if path == "/proc" {
			t.Error("a tool that asked for nothing was granted /proc, and with it every process's environment")
		}
	}

	asked := landlockPolicy(t, s.Wrap("/tools/web_fetch/run", "/w/S1", "/t", []string{"/proc", "/sys"}, nil))
	if !strings.Contains(strings.Join(asked.Read, " "), "/proc") {
		t.Errorf("read = %v, want the path the tool asked for", asked.Read)
	}
}

// landlockPolicy reads back the policy the wrapper carries.
func landlockPolicy(t *testing.T, argv []string) policy {
	t.Helper()
	var p policy
	if len(argv) < 3 {
		t.Fatalf("argv = %v, want a wrapper carrying a policy", argv)
	}
	if err := json.Unmarshal([]byte(argv[2]), &p); err != nil {
		t.Fatalf("policy is not readable: %v", err)
	}
	return p
}

// The manifest is the tool's whole declaration, so what it says about the paths
// it needs has to survive being read back.
func TestTheManifestReportsThePathsAToolAsksFor(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, DBPath(dir), nil)
	toolDir := filepath.Join(dir, "browser")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"browser","description":"Render a page.","db_prefix":"browser_",
	  "reads":["/proc"],"parameters":{"type":"object","properties":{}}}`
	if err := os.WriteFile(filepath.Join(toolDir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "run"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, failures := r.Load(nil); len(failures) > 0 {
		t.Fatalf("tool did not load: %+v", failures)
	}
	got := r.Get("browser")
	if got == nil {
		t.Fatal("the tool did not load")
	}
	if len(got.Reads) != 1 || got.Reads[0] != "/proc" {
		t.Errorf("reads = %v, want [/proc]", got.Reads)
	}
}

// Where nothing is enforcing, the line that says so has to be a warning. A
// container on a kernel without Landlock, or a platform with no mechanism at
// all, is a working agent with a boundary missing — and every other line at
// startup reports something that works.
func TestAnUnenforcedSandboxSaysSoAsAWarning(t *testing.T) {
	off := &Sandbox{Mechanism: "none", Reason: "this kernel has no Landlock"}
	line := off.Describe()
	if !strings.Contains(line, "WARNING") {
		t.Errorf("Describe() = %q, want it marked as a warning", line)
	}
	if !strings.Contains(line, "this kernel has no Landlock") {
		t.Errorf("Describe() = %q, want it to carry the reason", line)
	}

	on := &Sandbox{Mechanism: "landlock"}
	if strings.Contains(on.Describe(), "WARNING") {
		t.Errorf("Describe() = %q, want no warning when a boundary is in force", on.Describe())
	}
}
