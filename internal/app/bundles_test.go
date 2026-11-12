package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	shellServerOnce sync.Once
	shellServerPath string
	shellServerErr  error
)

// shellServer builds the stand-in MCP server once for the whole run.
func shellServer(t *testing.T) string {
	t.Helper()
	shellServerOnce.Do(func() {
		dir, err := os.MkdirTemp("", "shell-server")
		if err != nil {
			shellServerErr = err
			return
		}
		shellServerPath = filepath.Join(dir, "shell")
		if out, err := exec.Command("go", "build", "-o", shellServerPath, "../../testenv/mcp/shell").CombinedOutput(); err != nil {
			shellServerErr = &exec.Error{Name: string(out), Err: err}
		}
	})
	if shellServerErr != nil {
		t.Fatalf("building the test server: %v", shellServerErr)
	}
	return shellServerPath
}

const shellManifest = `{
  "manifest_version": "0.3",
  "name": "shell",
  "version": "1.2.3",
  "description": "A shell, for the tests.",
  "author": {"name": "the tests"},
  "server": {
    "type": "binary",
    "entry_point": "server/shell",
    "mcp_config": {"command": "${__dirname}/server/shell", "args": [], "env": {"FROM_MANIFEST": "${__dirname}"}}
  },
  "compatibility": {"platforms": ["linux"]},
  "_meta": {"io.github.jonaspf.agent": {"timeout_seconds": 2}}
}`

// writeBundle unpacks a bundle named name into dir, as an install leaves one:
// a folder holding manifest.json and the server it names.
func writeBundle(t *testing.T, dir, name, manifest string) string {
	t.Helper()
	b := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(b, "server"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(shellServer(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "server", "shell"), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

// bundleApp is a confined agent with no tools of the old kind and one
// directory of bundles, loaded.
func bundleApp(t *testing.T, bundles string) (*App, []LoadFailure) {
	t.Helper()
	a, _ := confinedApp(t, t.TempDir(), "", "")
	a.tools.bundleDirs = []string{bundles}
	_, failures := a.tools.Load(a)
	return a, failures
}

func listedTools(t *testing.T, a *App) (map[string]map[string]any, []LoadFailure) {
	t.Helper()
	w := callAPI(t, a, "GET", "/tools", nil)
	var out struct {
		Tools    []map[string]any `json:"tools"`
		Failures []LoadFailure    `json:"failures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]any{}
	for _, tool := range out.Tools {
		byName[tool["name"].(string)] = tool
	}
	return byName, out.Failures
}

// "A bundle's tools are the ones its server lists, described as the server
// describes them."
func TestABundlesToolsAreWhatItsServerLists(t *testing.T) {
	bundles := t.TempDir()
	writeBundle(t, bundles, "shell", shellManifest)
	a, failures := bundleApp(t, bundles)
	if len(failures) > 0 {
		t.Fatalf("the bundle did not load: %+v", failures)
	}
	tools, _ := listedTools(t, a)
	sh, ok := tools["sh"]
	if !ok {
		t.Fatalf("GET /tools has no sh: %v", tools)
	}
	if sh["description"] != "Run a shell command in the conversation's directory." {
		t.Errorf("description = %q, want the server's own", sh["description"])
	}
	params, _ := json.Marshal(sh["parameters"])
	if !strings.Contains(string(params), `"command"`) || !strings.Contains(string(params), `"object"`) {
		t.Errorf("parameters = %s, want the server's input schema", params)
	}
	if sh["bundle"] != "shell 1.2.3" {
		t.Errorf("bundle = %v, want the bundle's name and version", sh["bundle"])
	}
	// And the model is sent it.
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	sent := false
	for _, schema := range a.tools.SchemasFor(s) {
		sent = sent || schema.Function.Name == "sh"
	}
	if !sent {
		t.Error("the model is not sent the bundle's tool")
	}
}

// "A bundle's tool called in a turn runs in the conversation's directory, and
// reaches nothing outside it" — another conversation's files, the agent's
// settings, and the directory bundles are installed in included.
func TestABundlesToolRunsConfinedToItsConversation(t *testing.T) {
	bundles := t.TempDir()
	writeBundle(t, bundles, "shell", shellManifest)
	a, failures := bundleApp(t, bundles)
	if len(failures) > 0 {
		t.Fatalf("the bundle did not load: %+v", failures)
	}
	other, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	secret := filepath.Join(a.ensureWorkspace(other.ID), "secret.txt")
	if err := os.WriteFile(secret, []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := "echo mine > note.txt && cat note.txt; echo dir=$FROM_MANIFEST; " +
		"cat " + secret + " 2>&1; cat " + a.cfg.EnvFile + " 2>&1; touch " + bundles + "/planted 2>&1; true"
	args, _ := json.Marshal(map[string]string{"command": command})
	srv := scriptedModel(t, toolStep{tool: "sh", args: string(args)}, toolStep{text: "Done."})
	a.or.base = srv.URL
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err := a.SendUserMessage(s.ID, "write a note"); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)

	var res toolResult
	for _, e := range a.store.Entries(s.ID) {
		if e.Role == "tool" && e.ToolName == "sh" {
			_ = json.Unmarshal(e.ToolResult, &res)
		}
	}
	if !res.OK || !strings.Contains(res.Content, "mine") {
		t.Fatalf("result = %+v, want the command's output", res)
	}
	if got, _ := os.ReadFile(filepath.Join(a.ensureWorkspace(s.ID), "note.txt")); strings.TrimSpace(string(got)) != "mine" {
		t.Errorf("the note is not in the conversation's directory: %q", got)
	}
	if !strings.Contains(res.Content, "dir="+filepath.Join(bundles, "shell")) {
		t.Errorf("the manifest's variable did not arrive with the bundle's directory in it: %s", res.Content)
	}
	if strings.Contains(res.Content, "theirs") || strings.Contains(res.Content, "sk-secret") {
		t.Errorf("the tool read what is not its conversation's: %s", res.Content)
	}
	if _, err := os.Stat(filepath.Join(bundles, "planted")); err == nil {
		t.Error("the tool wrote into the directory bundles are installed in")
	}
}

// "A tool that reports an error, or outlives its bundle's timeout, is an
// error result the model reads, never a failed turn."
func TestABundlesFailuresAreResults(t *testing.T) {
	bundles := t.TempDir()
	writeBundle(t, bundles, "shell", shellManifest)
	a, _ := bundleApp(t, bundles)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	call := func(command string) toolResult {
		args, _ := json.Marshal(map[string]string{"command": command})
		return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "sh", args)
	}
	if res := call("echo broken; exit 3"); res.OK || !strings.Contains(res.Error, "broken") {
		t.Errorf("a failed command = %+v, want an error carrying what it said", res)
	}
	if res := call("sleep 30"); res.OK || !strings.Contains(res.Error, "2s timeout") {
		t.Errorf("a command past the bundle's timeout = %+v, want it named", res)
	}
	if res := call("echo fine"); !res.OK || !strings.Contains(res.Content, "fine") {
		t.Errorf("the call after a timeout = %+v, want it to work", res)
	}
}

// "A bundle that cannot run here is not loaded, and the Tools screen says
// why."
func TestABundleThatCannotRunIsRefusedWithAReason(t *testing.T) {
	bundles := t.TempDir()
	edit := func(old, new string) string {
		if !strings.Contains(shellManifest, old) {
			t.Fatalf("the manifest has no %q to change", old)
		}
		return strings.Replace(shellManifest, old, new, 1)
	}
	writeBundle(t, bundles, "good", edit(`"name": "shell"`, `"name": "good"`))
	writeBundle(t, bundles, "node", edit(`"type": "binary"`, `"type": "node"`))
	writeBundle(t, bundles, "mac", edit(`["linux"]`, `["darwin"]`))
	writeBundle(t, bundles, "climbs", edit("${__dirname}/server/shell", "${__dirname}/../good/server/shell"))
	writeBundle(t, bundles, "asks", edit(`"compatibility"`, `"user_config": {"key": {"type": "string", "required": true}}, "compatibility"`))
	// The same tool again, from a second bundle.
	writeBundle(t, bundles, "twin", edit(`"name": "shell"`, `"name": "twin"`))
	if err := os.MkdirAll(filepath.Join(bundles, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	a, _ := bundleApp(t, bundles)
	tools, failures := listedTools(t, a)
	if tools["sh"]["bundle"] != "good 1.2.3" {
		t.Errorf("sh comes from %v, want the first bundle that offered it", tools["sh"]["bundle"])
	}
	why := map[string]string{}
	for _, f := range failures {
		why[f.Dir] = f.Reason
	}
	for dir, want := range map[string]string{
		"node": "binary", "mac": "linux", "climbs": "inside the bundle",
		"asks": "key", "twin": "already", "empty": "manifest.json",
	} {
		if !strings.Contains(why[dir], want) {
			t.Errorf("%s: reason = %q, want it to mention %q", dir, why[dir], want)
		}
	}
}

// shippedBundles is where the bundles the image ships are unpacked for the
// tests: the ones plugins.lock names, fetched by `task plugins:fetch`, which
// every test task runs first. They are the real ones, so what is tested here
// is what runs.
func shippedBundles(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../plugins")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shell", "manifest.json")); err != nil {
		t.Fatalf("the shipped bundles are not in %s; run `task plugins:fetch`: %v", dir, err)
	}
	return dir
}
