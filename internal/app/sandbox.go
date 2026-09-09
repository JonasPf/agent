package app

import (
	"fmt"
	"os"
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
// no privileges: Seatbelt on macOS and Landlock on Linux. Where neither is
// available nothing is enforced, and that is said out loud rather than left to
// be discovered.
type Sandbox struct {
	// Mechanism is seatbelt, landlock, or none.
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

// linuxReads is the same set for Landlock, which grants access beneath a
// directory rather than matching a path, so the root itself is not among them:
// granting the root would grant everything under it.
var linuxReads = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt"}

// linuxDevices is what a program opens before any of its own code runs. They are
// named one by one rather than granting /dev, because a grant on the directory
// is a grant on every device in it.
var linuxDevices = []string{
	"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty",
}

func NewSandbox(cfg Config) *Sandbox {
	s := &Sandbox{
		workspaceRoot: absOr(cfg.Workspace),
		dataDir:       absOr(cfg.DataDir),
		toolsDir:      absOr(cfg.ToolsDir),
		dbPath:        absOr(DBPath(cfg.DataDir)),
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
		// Landlock rather than bubblewrap: it asks the kernel directly and needs
		// no namespace, so it works in an unprivileged container on a host that
		// refuses unprivileged user namespaces — which is where this runs.
		if ok, why := landlockAvailable(); ok {
			s.Mechanism = "landlock"
		} else {
			s.Mechanism, s.Reason = "none", why
		}
	default:
		s.Mechanism, s.Reason = "none", "no sandbox is implemented for "+runtime.GOOS
	}
	s.prepare()
	return s
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
	case "landlock":
		// The agent is its own wrapper: this re-runs the agent, which restricts
		// itself to the policy and then becomes the tool. Only the working
		// directory is writable; the runtime and the tool directory are
		// readable; nothing else is granted anything, so nothing else is
		// reachable — the home directory, another session's files, and every
		// transcript included.
		p := policy{
			// The working directory, and the directory the database lives in so
			// SQLite can create the journals it writes beside it.
			Write: []string{ws, filepath.Dir(s.dbPath)},
			Read:  append(append([]string{tools}, linuxReads...), s.ReadPaths...),
			// The device files a program opens before any of its own code runs.
			// bubblewrap gave a fresh /dev and this grants the same handful by
			// name: without /dev/null a shell cannot redirect, and every tool
			// fails on launch rather than on anything it was asked to do.
			Files: linuxDevices,
			Chdir: ws,
		}
		exe, err := os.Executable()
		if err != nil {
			// Without the wrapper there is no confinement, and a tool that runs
			// unconfined because the wrapper could not be found is the failure
			// this whole file exists to prevent.
			return []string{"/nonexistent/confinement-unavailable"}
		}
		return append([]string{exe, "-confine", p.encode(), "--"}, cmd...)
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
