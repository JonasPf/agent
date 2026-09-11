package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A session's working directory was isolated by convention: read, write, and
// edit refused a path outside it, and bash — a shell, which does what it is
// told — did not. One session's tools could read another's files and the
// operator's API key. The boundary is now the operating system's.
//
// The mechanism is Landlock, and there is only one. A second mechanism for a
// second platform means two policies to keep in step, and they do not stay in
// step: the macOS profile this replaces silently ignored the paths a tool
// declared in its manifest, which the Linux one honoured. The agent is a Linux
// program; where Landlock is not available nothing is enforced, and that is said
// out loud rather than left to be discovered.
type Sandbox struct {
	// Mechanism is landlock or none.
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
}

// linuxReads is the same set for Landlock, which grants access beneath a
// directory rather than matching a path, so the root itself is not among them:
// granting the root would grant everything under it.
//
// It does not include /proc. A browser needs it and nothing else does — the
// suite proved that by passing without it — and /proc is the one tree where a
// read grant is also a leak: /proc/<pid>/environ of any process this user owns
// is readable through it, the agent's included. A tool that needs it says so in
// its manifest, and only the two browser tools do.
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
	// No branch on the platform: the probe answers on every one of them, and
	// says why when the answer is no.
	if ok, why := landlockAvailable(); ok {
		s.Mechanism = "landlock"
	} else {
		s.Mechanism, s.Reason = "none", why
	}
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
	// Marked as a warning because it is one: every other line at startup reports
	// what is working. This reports that a boundary the rest of the system is
	// written around is absent, and it has to read as different from the rest.
	return "WARNING: sandbox NOT ENFORCED (" + s.Reason + ") — a tool can read and write anything this user can, " +
		"including every session's files and this process's own directory"
}

// Wrap returns the argv that runs bin, confined to workspace. toolRoot is the
// tool directory in force — a tool must be able to read its own executable and
// the helper beside it — and may be empty for a command that lives in the
// system paths, such as the shell a check runs in. It is a parameter rather
// than a field because the registry's directory is what is actually in use, and
// a sandbox pointed at the configured one would refuse a tool loaded from
// anywhere else.
// reads are the paths this tool asked for beyond the runtime, from its manifest.
func (s *Sandbox) Wrap(bin, workspace, toolRoot string, reads []string, args []string) []string {
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
			Read:  append(append(append([]string{tools}, linuxReads...), s.ReadPaths...), reads...),
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

// absOr resolves a path the way the kernel will see it. A rule is matched
// against the real path, so a policy written with an unresolved one silently
// matches nothing — which looks exactly like a sandbox that is working.
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
