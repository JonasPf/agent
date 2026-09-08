package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// A session's working directory was isolated by convention: read, write, and
// edit refused a path outside it, and bash — a shell, which does what it is
// told — did not. One session's tools could read another's files and the
// operator's API key. The boundary is now the operating system's.
//
// There is no cross-platform library for this. Every tool that claims to be one
// branches on the platform, so this does too, over the two primitives that need
// no privileges: Seatbelt on macOS and bubblewrap on Linux. Where neither is
// available nothing is enforced, and that is said out loud rather than left to
// be discovered.
type Sandbox struct {
	// Mechanism is seatbelt, bubblewrap, or none.
	Mechanism string `json:"mechanism"`
	// Reason is why nothing is enforced, when nothing is.
	Reason string `json:"reason,omitempty"`
	// ReadPaths is what the operator added beyond the runtime, so the boundary
	// in force can be read rather than inferred.
	ReadPaths []string `json:"read_paths,omitempty"`

	workspaceRoot string
	dataDir       string
	toolsDir      string
	dbPath        string
	profilePath   string
}

// systemReads is the runtime: the interpreter, its libraries, and what they
// load. It is the smallest set in which a tool starts at all, arrived at by
// running every supplied tool under the profile. The root directory is in it
// because Seatbelt cannot resolve any path without reading it.
var systemReads = []string{
	"/", "/usr", "/System", "/Library", "/bin", "/sbin", "/opt", "/dev",
	"/private/etc", "/private/var/db",
}

// linuxReads is the same set for bubblewrap, which binds directories rather
// than matching paths, so the root itself is not among them.
var linuxReads = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt"}

func NewSandbox(cfg Config) *Sandbox {
	s := &Sandbox{
		workspaceRoot: absOr(cfg.Workspace),
		dataDir:       absOr(cfg.DataDir),
		toolsDir:      absOr(cfg.ToolsDir),
		dbPath:        absOr(filepath.Join(cfg.DataDir, "agent.db")),
		ReadPaths:     readPaths(cfg.ReadPaths),
	}
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat("/usr/bin/sandbox-exec"); err == nil {
			s.Mechanism = "seatbelt"
		} else {
			s.Mechanism, s.Reason = "none", "/usr/bin/sandbox-exec is not present"
		}
	case "linux":
		bin, err := exec.LookPath("bwrap")
		if err != nil {
			s.Mechanism, s.Reason = "none", "bwrap is not installed; add the bubblewrap package to the image"
			break
		}
		// Installed is not the same as usable. A container on a host that
		// refuses unprivileged user namespaces has bwrap and cannot create one,
		// and claiming the boundary anyway is the worst of the three outcomes:
		// the interface shows a confinement that is not there, and every tool
		// dies on launch instead of running unconfined.
		if ok, why := probeBubblewrap(bin); ok {
			s.Mechanism = "bubblewrap"
		} else {
			s.Mechanism, s.Reason = "none", why
		}
	default:
		s.Mechanism, s.Reason = "none", "no sandbox is implemented for "+runtime.GOOS
	}
	s.prepare()
	return s
}

// probeBubblewrap runs the smallest sandbox there is, to find out whether this
// kernel will allow one at all. It costs a few milliseconds at startup, once,
// and it is the difference between a boundary and a claim about one.
func probeBubblewrap(bin string) (bool, string) {
	cmd := exec.Command(bin, "--ro-bind", "/", "/", "--dev", "/dev", "--tmpfs", "/tmp", "--", "/bin/true")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, ""
	}
	why := strings.TrimSpace(string(out))
	if why == "" {
		why = err.Error()
	}
	// One line, kept long enough to stay actionable: the kernel's own wording is
	// what tells the operator this is a host setting and not a missing package.
	why, _, _ = strings.Cut(why, "\n")
	// The reason is shown in a status panel and in a log line, so it names the
	// binary rather than reading as a missing package.
	return false, "bwrap is installed but cannot create a namespace here: " + truncate(strings.TrimSpace(why), 200)
}

func (s *Sandbox) Enforcing() bool { return s != nil && s.Mechanism != "" && s.Mechanism != "none" }

// Describe is the one line said at startup and shown in the interface.
func (s *Sandbox) Describe() string {
	if s.Enforcing() {
		line := fmt.Sprintf("sandbox: %s — a tool reads and writes its own session's directory and nothing else", s.Mechanism)
		if len(s.ReadPaths) > 0 {
			line += ", plus reads of " + strings.Join(s.ReadPaths, ", ")
		}
		return line
	}
	return "sandbox: NOT ENFORCED (" + s.Reason + ") — a tool can read and write anything this user can"
}

// prepare writes whatever the mechanism needs on disk. It runs as part of
// construction rather than as a second call, because a sandbox that was built
// but not prepared confines nothing and looks exactly like one that does. A
// failure downgrades to no enforcement, which is reported.
func (s *Sandbox) prepare() {
	if s.Mechanism != "seatbelt" {
		return
	}
	path := filepath.Join(s.dataDir, "sandbox.sb")
	if err := os.WriteFile(path, []byte(seatbeltProfile(s.ReadPaths)), 0o600); err != nil {
		s.Mechanism, s.Reason = "none", "could not write the Seatbelt profile: "+err.Error()
		return
	}
	s.profilePath = path
}

// Wrap returns the argv that runs bin, confined to workspace. toolRoot is the
// tool directory in force — a tool must be able to read its own executable and
// the helper beside it — and may be empty for a command that lives in the
// system paths, such as the shell a check runs in. It is a parameter rather
// than a field because the registry's directory is what is actually in use, and
// a sandbox pointed at the configured one would refuse a tool loaded from
// anywhere else.
func (s *Sandbox) Wrap(bin, workspace, toolRoot string, args []string) []string {
	cmd := append([]string{bin}, args...)
	if !s.Enforcing() {
		return cmd
	}
	ws := absOr(workspace)
	tools := s.toolsDir
	if toolRoot != "" {
		tools = absOr(toolRoot)
	}
	switch s.Mechanism {
	case "seatbelt":
		argv := []string{"/usr/bin/sandbox-exec",
			"-D", "WS=" + ws,
			"-D", "TOOLS=" + tools,
			// SQLite creates the journals beside the database, and a rule for
			// the database alone leaves a tool unable to open its own tables.
			"-D", "DB=" + s.dbPath,
			"-D", "DBWAL=" + s.dbPath + "-wal",
			"-D", "DBSHM=" + s.dbPath + "-shm",
			"-f", s.profilePath}
		return append(argv, cmd...)
	case "bubblewrap":
		// The tmpfs comes first, before every bind. bwrap applies its arguments in
		// order, so a tmpfs mounted later masks whatever is already beneath it —
		// and /tmp is where Linux puts a temporary directory, so a workspace or a
		// named read path can legitimately live there.
		argv := []string{"bwrap", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp"}
		// Only the runtime is bound, so the home directory and the repository
		// the agent runs from are not in the mount namespace at all.
		for _, p := range append(append([]string{}, linuxReads...), s.ReadPaths...) {
			if _, err := os.Stat(p); err == nil {
				argv = append(argv, "--ro-bind", p, p)
			}
		}
		argv = append(argv,
			"--ro-bind", tools, tools,
			"--bind", ws, ws)
		for _, f := range []string{s.dbPath, s.dbPath + "-wal", s.dbPath + "-shm"} {
			if _, err := os.Stat(f); err == nil {
				argv = append(argv, "--bind", f, f)
			}
		}
		argv = append(argv, "--die-with-parent", "--chdir", ws, "--")
		return append(argv, cmd...)
	}
	return cmd
}

// TempDir is the scratch directory a tool is given, inside its own working
// directory. The system one is not writable, and a scratch file that outlives
// the call should be as visible as anything else the session wrote.
func (s *Sandbox) TempDir(workspace string) string {
	return filepath.Join(workspace, ".tmp")
}

// readPaths resolves the operator's additions and drops what is not there, so a
// stale entry cannot make a profile that refuses to compile.
func readPaths(raw string) []string {
	var out []string
	for _, p := range filepath.SplitList(raw) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		if abs := absOr(p); abs != "" {
			if _, err := os.Stat(abs); err == nil {
				out = append(out, abs)
			}
		}
	}
	return out
}

// seatbeltProfile is the macOS policy. It is an allow-list: everything is
// denied, and what a tool needs in order to run is named. Nothing is written as
// a deny, so a path nobody thought of is refused rather than permitted.
func seatbeltProfile(extraReads []string) string {
	quoted := func(paths []string) string {
		var b strings.Builder
		for _, p := range paths {
			b.WriteString("\n       (subpath \"" + p + "\")")
		}
		return b.String()
	}
	// The root directory is a literal, not a subpath: a subpath of "/" would
	// allow the whole filesystem, which is the rule this replaces.
	system := `(allow file-read* (literal "/")` + quoted(systemReads[1:]) + `)`
	extra := ""
	if len(extraReads) > 0 {
		extra = "(allow file-read*" + quoted(extraReads) + ")\n"
	}
	return strings.Join([]string{
		`(version 1)`,
		`(deny default)`,
		`(allow process-exec process-fork signal sysctl-read mach-lookup`,
		`       network-outbound network-inbound system-socket ipc-posix-shm)`,
		// A browser is several processes that find each other through the
		// bootstrap server and talk to the graphics stack. Without these it dies
		// on its first instruction. Neither opens a path.
		`(allow iokit-open mach-register)`,
		// Metadata of what is walked through, so a path inside an allowed
		// subpath can be resolved. It says a path exists, never what is in it.
		`(allow file-read-metadata)`,
		system,
		extra + `(allow file-read* (subpath (param "TOOLS")))`,
		`(allow file-read* file-write* (literal (param "DB")) (literal (param "DBWAL"))`,
		`                              (literal (param "DBSHM")))`,
		`(allow file-write-data (literal "/dev/null") (literal "/dev/stdout")`,
		`                       (literal "/dev/stderr") (literal "/dev/dtracehelper"))`,
		// The one writable place. It is last only for readability; every rule
		// here allows, so order does not decide the outcome.
		`(allow file-read* file-write* (subpath (param "WS")))`,
		``,
	}, "\n")
}

// absOr resolves a path the way the sandbox will see it. Both mechanisms match
// on the real path, and on macOS /var and /tmp are symlinks into /private — so
// a profile written with the unresolved path silently matches nothing, which
// looks exactly like a sandbox that is working.
func absOr(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}
