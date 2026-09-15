package app

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The filesystem boundary kept a tool out of other sessions' files. It did not
// keep a tool off the operator's API, which is a port on the same machine and
// can create a conversation holding a credential. So a tool may open TCP
// connections only to the ports software commonly talks on, the operator's is
// not among them, and the operator's is one nothing else commonly uses.

func policyOf(t *testing.T, argv []string) policy {
	t.Helper()
	if len(argv) < 4 || argv[1] != "-confine" {
		t.Fatalf("argv = %v, want the wrapper in front", argv)
	}
	var p policy
	if err := json.Unmarshal([]byte(argv[2]), &p); err != nil {
		t.Fatalf("policy is not readable: %v", err)
	}
	return p
}

func hasPort(ports []int, want int) bool {
	for _, p := range ports {
		if p == want {
			return true
		}
	}
	return false
}

func TestToolsMayConnectToTheCommonPortsAndNotTheOperators(t *testing.T) {
	s := NewSandbox(Config{Addr: ":7770"})
	s.Mechanism, s.Network = "landlock", "landlock"
	s.SetToolAPIPort(45123)

	p := policyOf(t, s.Wrap("/t/x/run", "/w/S1", "/t", nil, nil))
	if !p.Net {
		t.Fatal("the policy does not restrict the network")
	}
	for _, want := range []int{22, 53, 80, 443, 5432, 8080, 45123} {
		if !hasPort(p.Ports, want) {
			t.Errorf("ports = %v, want %d among them", p.Ports, want)
		}
	}
	if hasPort(p.Ports, 7770) {
		t.Errorf("ports = %v include the operator's 7770", p.Ports)
	}
}

// Moving the operator onto a port tools commonly use takes it away from them,
// and says so, rather than leaving a hole where the boundary was.
func TestAnOperatorOnACommonPortTakesItFromTools(t *testing.T) {
	s := NewSandbox(Config{Addr: "127.0.0.1:8080"})
	if hasPort(s.Ports, 8080) {
		t.Errorf("ports = %v include the operator's own 8080", s.Ports)
	}
	if !strings.Contains(s.PortsNote, "8080") {
		t.Errorf("nothing says 8080 was taken away from tools: %q", s.PortsNote)
	}
}

func TestNamedToolPortsOnlyAdd(t *testing.T) {
	s := NewSandbox(Config{Addr: ":7770", ToolPorts: "2222, 9000 nonsense 70000 7770"})
	for _, want := range []int{2222, 9000, 443} {
		if !hasPort(s.Ports, want) {
			t.Errorf("ports = %v, want %d", s.Ports, want)
		}
	}
	for _, refused := range []int{70000, 7770} {
		if hasPort(s.Ports, refused) {
			t.Errorf("ports = %v include %d", s.Ports, refused)
		}
	}
}

func TestTheOperatorIsNotOnAPortToolsUse(t *testing.T) {
	t.Setenv("AGENT_ADDR", "")
	os.Unsetenv("AGENT_ADDR")
	_, port, err := net.SplitHostPort(LoadConfig().Addr)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	if hasPort(defaultToolPorts, n) {
		t.Errorf("the operator listens on %d by default, which tools commonly use", n)
	}
}

// A kernel with filesystem rules and no network rules still confines files.
// It must say the network half is missing rather than claim the whole.
func TestWithoutNetworkRulesTheSandboxSaysSo(t *testing.T) {
	s := &Sandbox{Mechanism: "landlock", Network: "none", NetworkReason: "this kernel predates network rules"}
	if !strings.Contains(s.Describe(), "network NOT ENFORCED") {
		t.Errorf("describe = %q, want the missing network rules said", s.Describe())
	}
	if p := policyOf(t, s.Wrap("/t/x/run", "/w/S1", "/t", nil, nil)); p.Net {
		t.Error("the policy asks for network rules the kernel cannot apply")
	}
}

// The boundary itself, from inside a confined process: an allowed port
// connects, the operator's is refused by the kernel.
func TestAConfinedToolReachesAnAllowedPortAndNotTheOperators(t *testing.T) {
	operator := httptest.NewServer(http.NotFoundHandler())
	defer operator.Close()
	allowed := httptest.NewServer(http.NotFoundHandler())
	defer allowed.Close()
	_, allowedPort, _ := net.SplitHostPort(allowed.Listener.Addr().String())

	dir := t.TempDir()
	// The policy makes the database's directory writable, and a writable path
	// has to exist; OpenStore makes it in every real agent.
	if err := os.MkdirAll(filepath.Dir(DBPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewSandbox(Config{DataDir: dir, Workspace: dir, ToolsDir: dir,
		Addr: operator.Listener.Addr().String(), ToolPorts: allowedPort})
	confinementOrSkip(t, s)
	if s.Network != "landlock" {
		notRun(t, "the kernel has no Landlock network rules (%s)", s.NetworkReason)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dial := func(addr string) (bool, string) {
		argv := s.Wrap(exe, dir, filepath.Dir(exe), nil, []string{"-dial", addr})
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		return err == nil, string(out)
	}
	if ok, out := dial(allowed.Listener.Addr().String()); !ok {
		t.Errorf("a confined process could not reach an allowed port: %s", out)
	}
	// Refused by the kernel, not failed to start: a wrapper that could not
	// confine at all also exits non-zero, and would pass this silently.
	if ok, out := dial(operator.Listener.Addr().String()); ok || !strings.Contains(out, "refused:") {
		t.Errorf("a confined process was not refused the operator's port: ok=%v %s", ok, out)
	}
}

// What the Tools screen says a tool may reach is what the wrapper applies,
// field for field, not a second description that can drift from it.
func TestTheToolsScreenShowsThePolicyEachToolRunsUnder(t *testing.T) {
	a := newTestApp(t)
	installEnvTool(t, a, "envcheck")
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("%+v", failures)
	}
	a.sandbox.Mechanism, a.sandbox.Network = "landlock", "landlock"
	a.sandbox.SetToolAPIPort(45123)

	w := call(t, a, "GET", "/tools", "")
	var got struct {
		Tools []struct {
			Name    string     `json:"name"`
			Builtin bool       `json:"builtin"`
			Reach   *ToolReach `json:"reach"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var reach *ToolReach
	for _, tl := range got.Tools {
		if tl.Name == "envcheck" {
			reach = tl.Reach
		}
		if tl.Builtin && tl.Reach != nil {
			t.Errorf("builtin %s reports a sandbox it does not run in", tl.Name)
		}
	}
	if reach == nil {
		t.Fatal("the tool reports no reach")
	}

	ws := "/w/S1"
	p := policyOf(t, a.sandbox.Wrap("/x/run", ws, a.tools.dir, a.tools.Get("envcheck").Reads, nil))
	if !reach.Enforced || !reach.NetworkEnforced {
		t.Errorf("reach = %+v, want both halves enforced", reach)
	}
	if strings.Join(reach.Read, "|") != strings.Join(p.Read, "|") {
		t.Errorf("read = %v, policy reads %v", reach.Read, p.Read)
	}
	if strings.Join(reach.Files, "|") != strings.Join(p.Files, "|") {
		t.Errorf("files = %v, policy files %v", reach.Files, p.Files)
	}
	wantWrite := strings.Replace(strings.Join(p.Write, "|"), ws, sessionDirLabel, 1)
	if strings.Join(reach.ReadWrite, "|") != wantWrite {
		t.Errorf("read_write = %v, policy writes %v", reach.ReadWrite, p.Write)
	}
	if strings.Trim(strings.Join(strings.Fields(strings.Trim(jsonInts(reach.Ports), "[]")), ""), ",") !=
		strings.Trim(strings.Join(strings.Fields(strings.Trim(jsonInts(p.Ports), "[]")), ""), ",") {
		t.Errorf("ports = %v, policy ports %v", reach.Ports, p.Ports)
	}
	if reach.OperatorPort != 0 && hasPort(reach.Ports, reach.OperatorPort) {
		t.Errorf("the screen lists the operator's port %d as reachable", reach.OperatorPort)
	}
}

func jsonInts(v []int) string {
	b, _ := json.Marshal(v)
	return string(b)
}
