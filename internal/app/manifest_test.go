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

// installManifest writes a tool directory holding the manifest given and a run
// that answers every call, and reloads the registry the way the operator does.
func installManifest(t *testing.T, a *App, dir, manifest string) map[string]any {
	t.Helper()
	d := filepath.Join(a.tools.dir, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	run := "#!/bin/sh\ncat >/dev/null\nprintf '{\"ok\":true,\"content\":\"done\"}'\n"
	if err := os.WriteFile(filepath.Join(d, "run"), []byte(run), 0o755); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/tools/reload", nil))
	if w.Code != 200 {
		t.Fatalf("reload answered %d: %s", w.Code, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
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

func failureFor(reply map[string]any, dir string) string {
	fs, _ := reply["failures"].([]any)
	for _, x := range fs {
		if m := x.(map[string]any); m["dir"] == dir {
			return m["reason"].(string)
		}
	}
	return ""
}

const deploysManifest = `{
  "name": "deploys",
  "description": "Query deployment status.",
  "parameters": {"type": "object", "properties": {"service": {"type": "string"}}, "required": ["service"]},
  "agent": {"db_prefix": "deploys_", "timeout_seconds": 45, "reads": ["/proc"], "env": ["DEPLOY_TOKEN"]}
}`

// The settings only this agent reads sit under "agent", and the registry runs
// the tool by them.
func TestAManifestKeepsTheAgentsOwnSettingsUnderAgent(t *testing.T) {
	a := newTestApp(t)
	if reply := installManifest(t, a, "deploys", deploysManifest); failureFor(reply, "deploys") != "" {
		t.Fatalf("the tool did not load: %s", failureFor(reply, "deploys"))
	}
	got := a.tools.Get("deploys")
	if got == nil {
		t.Fatal("the tool is not registered")
	}
	want := ToolAgent{DBPrefix: "deploys_", Timeout: 45, Reads: []string{"/proc"}, Env: []string{"DEPLOY_TOKEN"}}
	if !reflect.DeepEqual(got.Agent, want) {
		t.Errorf("agent = %+v, want %+v", got.Agent, want)
	}
	agent, _ := screenTool(t, toolsScreen(t, a), "deploys")["agent"].(map[string]any)
	if agent["db_prefix"] != "deploys_" || agent["timeout_seconds"] != 45.0 {
		t.Errorf("the screen shows agent = %v", agent)
	}
	if env, _ := agent["env"].([]any); len(env) != 1 || env[0] != "DEPLOY_TOKEN" {
		t.Errorf("the screen shows env = %v", agent["env"])
	}
}

// The model is sent the standard part and nothing of the agent's own, and the
// screen shows that entry as the model receives it.
func TestWhatTheModelIsSentForAToolIsItsStandardPart(t *testing.T) {
	a := newTestApp(t)
	installManifest(t, a, "deploys", deploysManifest)
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
	for _, own := range []string{"agent", "db_prefix", "timeout_seconds", "DEPLOY_TOKEN", "/proc"} {
		if strings.Contains(string(b), own) {
			t.Errorf("the model is sent %q: %s", own, b)
		}
	}
	if shown := screenTool(t, toolsScreen(t, a), "deploys")["sent"]; !reflect.DeepEqual(shown, asSent) {
		t.Errorf("the screen shows %v as sent; the model is sent %v", shown, asSent)
	}
}

// A setting written where the model would be sent it, or misspelt where the
// agent would ignore it, stops the tool loading and is named; the other tools
// keep loading.
func TestAFieldOutsideTheManifestStopsThatToolLoading(t *testing.T) {
	cases := map[string]struct{ manifest, field string }{
		"old shape": {`{"name":"old","description":"Old.","db_prefix":"old_","timeout_seconds":30,
		  "parameters":{"type":"object","properties":{}}}`, `"db_prefix"`},
		"misspelt": {`{"name":"old","description":"Old.","parameters":{"type":"object","properties":{}},
		  "agent":{"db_prefix":"old_","timeout":5}}`, `"timeout"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			a := newTestApp(t)
			installManifest(t, a, "deploys", deploysManifest)
			reply := installManifest(t, a, "old", c.manifest)
			reason := failureFor(reply, "old")
			if reason == "" {
				t.Fatalf("a manifest with %s loaded", c.field)
			}
			if !strings.Contains(reason, c.field) {
				t.Errorf("the reason %q does not name %s", reason, c.field)
			}
			if a.tools.Get("old") != nil {
				t.Error("the tool was registered anyway")
			}
			if a.tools.Get("deploys") == nil {
				t.Error("another tool stopped loading")
			}
		})
	}
}

func TestAManifestWithoutATablePrefixDoesNotLoad(t *testing.T) {
	a := newTestApp(t)
	reply := installManifest(t, a, "bare", `{"name":"bare","description":"Bare.",
	  "parameters":{"type":"object","properties":{}},"agent":{"timeout_seconds":5}}`)
	if reason := failureFor(reply, "bare"); !strings.Contains(reason, "db_prefix") {
		t.Errorf("reason = %q, want it to name agent.db_prefix", reason)
	}
}

// The boundary is the same for every tool program, so the screen is given it
// once, and each tool says only what its manifest adds.
func TestTheToolsScreenIsGivenTheSandboxOnce(t *testing.T) {
	a := newTestApp(t)
	installManifest(t, a, "deploys", deploysManifest)
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

// Every manifest the image ships is in the shape the registry reads.
func TestEverySuppliedManifestLoads(t *testing.T) {
	root := filepath.Join("..", "..", "tools")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := loadToolDir(filepath.Join(root, e.Name())); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
	}
}

// A parameter without a description reaches the model as a bare name and a
// type, so every tool the image ships describes each one.
func TestEverySuppliedParameterIsDescribed(t *testing.T) {
	root := filepath.Join("..", "..", "tools")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tl, err := loadToolDir(filepath.Join(root, e.Name()))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		props, _ := tl.Parameters["properties"].(map[string]any)
		for name, p := range props {
			if d, _ := p.(map[string]any)["description"].(string); strings.TrimSpace(d) == "" {
				t.Errorf("%s.%s has no description", tl.Name, name)
			}
		}
	}
}
