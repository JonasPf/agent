package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// installDeploys installs a bundle whose one tool, deploys, asks for a path
// to read and a timeout of its own, and reloads the registry the way the
// operator does.
func installDeploys(t *testing.T, a *App) {
	t.Helper()
	dir := t.TempDir()
	d := writeBundle(t, dir, "deploys", `{
  "manifest_version": "0.3", "name": "deploys", "version": "0.0.1",
  "description": "Deployments.", "author": {"name": "the tests"},
  "server": {"type": "binary", "entry_point": "server/shell",
    "mcp_config": {"command": "${__dirname}/server/shell",
      "args": ["deploys", "Query deployment status.", "${__dirname}/run"], "env": {"DEPLOY_TOKEN": "t0ken"}}},
  "_meta": {"io.github.jonaspf.agent": {"timeout_seconds": 45, "reads": ["/proc"]}}
}`)
	run := "#!/bin/sh\ncat >/dev/null\nprintf done\n"
	if err := os.WriteFile(filepath.Join(d, "run"), []byte(run), 0o755); err != nil {
		t.Fatal(err)
	}
	a.tools.bundleDirs = []string{dir}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/tools/reload", nil))
	if w.Code != 200 || a.tools.Get("deploys") == nil {
		t.Fatalf("reload answered %d: %s", w.Code, w.Body)
	}
}

// toolsScreen is what the Tools screen reads.
func toolsScreen(t *testing.T, a *App) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/tools", nil))
	if w.Code != 200 {
		t.Fatalf("GET /tools answered %d: %s", w.Code, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func screenTool(t *testing.T, screen map[string]any, name string) map[string]any {
	t.Helper()
	for _, x := range screen["tools"].([]any) {
		if m := x.(map[string]any); m["name"] == name {
			return m
		}
	}
	t.Fatalf("%s is not on the Tools screen: %v", name, screen["tools"])
	return nil
}

// What only this agent reads sits under _meta in a bundle's manifest, and the
// registry runs the tool by it.
func TestABundleKeepsTheAgentsOwnSettingsUnderMeta(t *testing.T) {
	a := newTestApp(t)
	installDeploys(t, a)
	got := a.tools.Get("deploys")
	want := ToolAgent{Timeout: 45, Reads: []string{"/proc"}}
	if !reflect.DeepEqual(got.Agent, want) {
		t.Errorf("agent = %+v, want %+v", got.Agent, want)
	}
	agent, _ := screenTool(t, toolsScreen(t, a), "deploys")["agent"].(map[string]any)
	if agent["timeout_seconds"] != 45.0 {
		t.Errorf("the screen shows agent = %v", agent)
	}
	if reads, _ := agent["reads"].([]any); len(reads) != 1 || reads[0] != "/proc" {
		t.Errorf("the screen shows reads = %v", agent["reads"])
	}
}

// The model is sent the standard part and nothing of the agent's own, and the
// screen shows that entry as the model receives it.
func TestWhatTheModelIsSentForAToolIsItsStandardPart(t *testing.T) {
	a := newTestApp(t)
	installDeploys(t, a)
	var sent ToolSchema
	for _, s := range a.tools.SchemasFor(nil) {
		if s.Function.Name == "deploys" {
			sent = s
		}
	}
	b, _ := json.Marshal(sent)
	var asSent map[string]any
	if err := json.Unmarshal(b, &asSent); err != nil {
		t.Fatal(err)
	}
	fn, _ := asSent["function"].(map[string]any)
	if asSent["type"] != "function" || fn["name"] != "deploys" || fn["description"] != "Query deployment status." || fn["parameters"] == nil {
		t.Fatalf("the model is sent %s", b)
	}
	for _, own := range []string{"agent", "timeout_seconds", "DEPLOY_TOKEN", "t0ken", "/proc"} {
		if strings.Contains(string(b), own) {
			t.Errorf("the model is sent %q: %s", own, b)
		}
	}
	if shown := screenTool(t, toolsScreen(t, a), "deploys")["sent"]; !reflect.DeepEqual(shown, asSent) {
		t.Errorf("the screen shows %v as sent; the model is sent %v", shown, asSent)
	}
}

// The boundary is the same for every tool program, so the screen is given it
// once, and each tool says only what its manifest adds.
func TestTheToolsScreenIsGivenTheSandboxOnce(t *testing.T) {
	a := newTestApp(t)
	installDeploys(t, a)
	screen := toolsScreen(t, a)
	box, _ := screen["sandbox"].(map[string]any)
	if box == nil {
		t.Fatalf("the screen is not given the sandbox: %v", screen)
	}
	read, _ := box["read"].([]any)
	for _, p := range read {
		if p == "/proc" {
			t.Errorf("what every tool gets includes /proc, which only deploys asked for: %v", read)
		}
	}
	if len(read) == 0 {
		t.Error("the sandbox reads nothing")
	}
	if rw, _ := box["read_write"].([]any); len(rw) == 0 {
		t.Error("the sandbox writes nothing")
	}
	if _, ok := screenTool(t, screen, "deploys")["reach"]; ok {
		t.Error("each tool still carries the whole sandbox")
	}
	builtin := screenTool(t, screen, "reload_tools")
	if _, ok := builtin["agent"]; ok {
		t.Error("a builtin has no manifest, and is shown an agent block")
	}
	if builtin["sent"] == nil {
		t.Error("a builtin is sent to the model too, and the screen does not show it")
	}
}

// Every bundle the image ships loads, and describes each parameter of each
// tool: one without a description reaches the model as a bare name and a type.
func TestEveryShippedBundleLoadsAndDescribesItsParameters(t *testing.T) {
	a := newTestApp(t)
	a.tools.bundleDirs = []string{shippedBundles(t)}
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("shipped bundles did not load: %+v", failures)
	}
	n := 0
	for _, tl := range a.tools.All() {
		if tl.Builtin {
			continue
		}
		n++
		props, _ := tl.Parameters["properties"].(map[string]any)
		for name, p := range props {
			if d, _ := p.(map[string]any)["description"].(string); strings.TrimSpace(d) == "" {
				t.Errorf("%s.%s has no description", tl.Name, name)
			}
		}
	}
	if n == 0 {
		t.Error("no shipped bundle offers a tool")
	}
}

// What the scheduling skill alone said is in the tool's description, which a
// conversation always has (ADR-078).
func TestTheScheduleToolSaysHowAJobBehaves(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("builtin", "schedule", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"with bash before creating the job", "killed at sixty seconds",
		"counts as a failure", "Nothing expires a job", "a running job can delete itself"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the schedule tool does not say %q", want)
		}
	}
}
