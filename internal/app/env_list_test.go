package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A grant is chosen from the names the agent could hand over, rather than typed
// from memory. The list is names alone: a value never leaves the agent this
// way, and the model key is not offered at all.

type grantable struct {
	Name        string `json:"name"`
	FromEnvFile bool   `json:"from_env_file"`
}

func listGrantable(t *testing.T, a *App) (map[string]grantable, string) {
	t.Helper()
	w := call(t, a, "GET", "/env", "")
	if w.Code != 200 {
		t.Fatalf("GET /env: %d %s", w.Code, w.Body.String())
	}
	var list []grantable
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	out := map[string]grantable{}
	for _, g := range list {
		out[g.Name] = g
	}
	return out, w.Body.String()
}

func TestTheGrantableNamesAreListedWithoutTheirValues(t *testing.T) {
	a := newTestApp(t)
	envFile := filepath.Join(t.TempDir(), ".env")
	file := "# credentials\nGH_TOKEN=ghp_from_file\nAGENT_REPO=https://tok@example.com/agent.git\n" +
		"OPENROUTER_API_KEY=sk-or-from-file\n"
	if err := os.WriteFile(envFile, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.EnvFile = envFile
	t.Setenv("GH_TOKEN", "ghp_in_environment")
	t.Setenv("SOME_SERVICE_KEY", "svc-in-environment")
	t.Setenv("OPENROUTER_API_KEY", "sk-or-in-environment")
	t.Setenv("AGENT_URL", "http://overwritten-per-call")

	names, body := listGrantable(t, a)

	for _, secret := range []string{"ghp_from_file", "tok@example.com", "sk-or-from-file",
		"ghp_in_environment", "svc-in-environment", "sk-or-in-environment"} {
		if strings.Contains(body, secret) {
			t.Errorf("the list carries a value: %q", secret)
		}
	}
	for name, fromFile := range map[string]bool{"GH_TOKEN": true, "AGENT_REPO": true, "SOME_SERVICE_KEY": false} {
		g, ok := names[name]
		if !ok {
			t.Errorf("%s is not offered", name)
			continue
		}
		if g.FromEnvFile != fromFile {
			t.Errorf("%s from_env_file = %v, want %v", name, g.FromEnvFile, fromFile)
		}
	}
	// The key cannot be granted, every tool already has PATH and HOME, and the
	// per-call contract is overwritten on every call: offering any of them would
	// offer a choice that does nothing.
	for _, name := range []string{"OPENROUTER_API_KEY", "PATH", "HOME", "AGENT_URL", "TMPDIR"} {
		if _, ok := names[name]; ok {
			t.Errorf("%s is offered as a grant", name)
		}
	}
}

// What the list offers is what a grant delivers: a name ticked from it reaches
// a tool run in that conversation.
func TestANameFromTheListReachesTheToolItIsGrantedTo(t *testing.T) {
	t.Setenv("SOME_SERVICE_KEY", "svc-in-environment")
	a := newTestApp(t)
	installEnvTool(t, a, "envcheck")

	names, _ := listGrantable(t, a)
	if _, ok := names["SOME_SERVICE_KEY"]; !ok {
		t.Fatal("SOME_SERVICE_KEY is not offered")
	}
	if !toolEnv(t, a, "envcheck", "SOME_SERVICE_KEY")["SOME_SERVICE_KEY"] {
		t.Error("a name offered by the list did not reach the tool it was granted to")
	}
}
