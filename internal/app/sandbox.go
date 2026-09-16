package app

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
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

	// Network is landlock when the kernel restricts which TCP ports a tool may
	// connect to, and none when it cannot — a kernel whose Landlock predates
	// network rules still confines files, and says the other half is missing.
	Network       string `json:"network"`
	NetworkReason string `json:"network_reason,omitempty"`
	// Ports are the TCP ports a tool may connect to: the common ones, plus what
	// the operator named in AGENT_TOOL_PORTS, never the operator's own.
	Ports []int `json:"ports"`
	// PortsNote says a port was taken away from tools because the operator
	// listens on it, so a missing port is explained rather than discovered.
	PortsNote    string `json:"ports_note,omitempty"`
	OperatorPort int    `json:"operator_port,omitempty"`
	// ToolAPIPort is where a tool reaches the agent. It is chosen when the
	// agent starts, so it is allowed here rather than listed above.
	ToolAPIPort int `json:"tool_api_port,omitempty"`

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

// defaultToolPorts are the TCP ports a tool may connect to without the operator
// naming them: what software commonly talks on. The operator's own port is not
// one of them, and is chosen so that it is not — a tool that could reach it
// could create a conversation holding a credential.
var defaultToolPorts = []int{
	22,      // ssh, and git over it
	53,      // DNS, when an answer is too large for UDP
	80, 443, // the web
	3000, 4000, 5000, 5173, 8000, 8080, 8443, 8888, // development servers
	3306, 5432, 6379, 27017, // MySQL, Postgres, Redis, MongoDB
	9418, // git's own protocol
}

// sessionDirLabel stands for the working directory in a description that is
// about every session rather than one.
const sessionDirLabel = "this session's working directory"

func NewSandbox(cfg Config) *Sandbox {
	s := &Sandbox{
		workspaceRoot: absOr(cfg.Workspace),
		dataDir:       absOr(cfg.DataDir),
		toolsDir:      absOr(cfg.ToolsDir),
		dbPath:        absOr(DBPath(cfg.DataDir)),
		ReadPaths:     readPaths(cfg.ReadPaths),
		OperatorPort:  portOf(cfg.Addr),
	}
	s.Ports, s.PortsNote = toolPorts(cfg.ToolPorts, s.OperatorPort)
	// No branch on the platform: the probe answers on every one of them, and
	// says why when the answer is no.
	if ok, why := landlockAvailable(); ok {
		s.Mechanism = "landlock"
	} else {
		s.Mechanism, s.Reason = "none", why
	}
	if ok, why := landlockNetAvailable(); ok {
		s.Network = "landlock"
	} else {
		s.Network, s.NetworkReason = "none", why
	}
	return s
}

func portOf(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

// toolPorts is the common ports plus the operator's additions, less the port
// the operator listens on. Anything that is not a port is dropped, the way a
// read path that is not there is: a typo must not stop tools from running.
func toolPorts(extra string, operator int) ([]int, string) {
	seen := map[int]bool{}
	var out []int
	note := ""
	add := func(n int) {
		if n < 1 || n > 65535 || seen[n] {
			return
		}
		if n == operator {
			note = fmt.Sprintf("port %d is the operator's, so no tool may connect to it", n)
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	for _, p := range defaultToolPorts {
		add(p)
	}
	for _, f := range strings.FieldsFunc(extra, func(r rune) bool { return r == ',' || r == ':' || unicode.IsSpace(r) }) {
		if n, err := strconv.Atoi(f); err == nil {
			add(n)
		}
	}
	sort.Ints(out)
	return out, note
}

// SetToolAPIPort records where tools reach the agent, once it is listening.
func (s *Sandbox) SetToolAPIPort(port int) { s.ToolAPIPort = port }

// connectPorts is every port the policy lets a tool open.
func (s *Sandbox) connectPorts() []int {
	out := append([]int(nil), s.Ports...)
	if s.ToolAPIPort > 0 && !hasInt(out, s.ToolAPIPort) {
		out = append(out, s.ToolAPIPort)
	}
	return out
}

func hasInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

func (s *Sandbox) Enforcing() bool { return s != nil && s.Mechanism != "" && s.Mechanism != "none" }

// Describe is the one line said at startup and shown in the interface.
func (s *Sandbox) Describe() string {
	if s.Enforcing() {
		line := fmt.Sprintf("sandbox: %s — a tool reads and writes its own session's directory and nothing else", s.Mechanism)
		if len(s.ReadPaths) > 0 {
			line += ", plus reads of " + strings.Join(s.ReadPaths, ", ")
		}
		if s.Network == "landlock" {
			line += fmt.Sprintf("; it opens TCP connections to %d named ports and the tool API, never the operator's port %d",
				len(s.Ports), s.OperatorPort)
		} else {
			line += "; network NOT ENFORCED (" + s.NetworkReason + ") — a tool can connect to any port, the operator's API included"
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
		p := s.policyFor(ws, tools, reads)
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

// policyFor is the policy a tool runs under. The wrapper applies it and the
// Tools screen shows it, from this one place, so what is said is what is done.
func (s *Sandbox) policyFor(ws, tools string, reads []string) policy {
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
	// Ports are an allow-list like paths: a port nobody named is refused. It
	// is asked for only where the kernel can apply it, because a policy the
	// wrapper cannot apply stops every tool from starting.
	if s.Network == "landlock" {
		p.Net = true
		p.Ports = s.connectPorts()
	}
	return p
}

// ToolReach is what one tool may touch, as the Tools screen shows it.
type ToolReach struct {
	Enforced  bool     `json:"enforced"`
	Reason    string   `json:"reason,omitempty"`
	ReadWrite []string `json:"read_write"`
	Read      []string `json:"read"`
	// ToolRead is the part of Read this tool's manifest asked for. The rest is
	// what every tool gets, and the screen says which is which.
	ToolRead        []string `json:"tool_read"`
	Files           []string `json:"files"`
	NetworkEnforced bool     `json:"network_enforced"`
	NetworkReason   string   `json:"network_reason,omitempty"`
	Ports           []int    `json:"ports"`
	ToolAPIPort     int      `json:"tool_api_port,omitempty"`
	OperatorPort    int      `json:"operator_port,omitempty"`
}

// Reach describes the policy a tool runs under. Where nothing is enforced it
// still says what the policy is — the one the container applies — beside the
// reason it does not apply here.
func (s *Sandbox) Reach(toolRoot string, reads []string) *ToolReach {
	tools := s.toolsDir
	if toolRoot != "" {
		tools = absOr(toolRoot)
	}
	p := s.policyFor(sessionDirLabel, tools, reads)
	r := &ToolReach{Enforced: s.Enforcing(), Reason: s.Reason, ReadWrite: p.Write, Read: p.Read,
		ToolRead: append([]string{}, reads...), Files: p.Files, NetworkEnforced: s.Enforcing() && s.Network == "landlock",
		Ports: s.connectPorts(), ToolAPIPort: s.ToolAPIPort, OperatorPort: s.OperatorPort}
	if !r.NetworkEnforced {
		r.NetworkReason = s.NetworkReason
		if !s.Enforcing() {
			r.NetworkReason = s.Reason
		}
	}
	return r
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
