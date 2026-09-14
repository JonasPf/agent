package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tool is a subprocess of the agent, and a subprocess inherits its parent's
// environment unless something stops it. That put the model API key in front of
// every tool ever written, including the one whose whole purpose is to run
// commands the model composed. The sandbox hides the file the key is read from
// and never hid the variable it was read into.
//
// So a tool is given the environment it needs and nothing else, and a credential
// beyond that reaches it only because a conversation was granted it.

// installEnvTool writes a tool that reports the environment it was started
// with. It is a real subprocess, launched through the registry the agent uses.
func installEnvTool(t *testing.T, a *App, name string) {
	t.Helper()
	dir := filepath.Join(a.tools.dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"name": name, "description": "Report the environment.", "db_prefix": name + "_",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
	}
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	// cat swallows the arguments on stdin; jq is not available, so the dump is
	// assembled with the shell's own quoting of a newline-free list of names.
	script := "#!/bin/sh\ncat >/dev/null\nnames=$(env | cut -d= -f1 | tr '\\n' ' ')\n" +
		"printf '{\"ok\":true,\"content\":\"%s\"}' \"$names\"\n"
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// toolEnv runs the named tool in a session granted grants, and returns the
// variable names the subprocess actually saw.
func toolEnv(t *testing.T, a *App, name string, grants ...string) map[string]bool {
	t.Helper()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tool did not load: %+v", failures)
	}
	s, _ := a.NewSession(SessionConfig{Model: "test/model", GrantedEnv: grants}, "")
	res := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, name, json.RawMessage(`{}`))
	if !res.OK {
		t.Fatalf("%s: %s (stderr %s)", name, res.Error, res.stderr)
	}
	seen := map[string]bool{}
	for _, v := range strings.Fields(res.Content) {
		seen[v] = true
	}
	return seen
}

func TestAToolNeverSeesTheModelKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-should-not-be-visible")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "also-not")
	a := newTestApp(t)
	installEnvTool(t, a, "envcheck")

	seen := toolEnv(t, a, "envcheck")
	for _, secret := range []string{"OPENROUTER_API_KEY", "AWS_SECRET_ACCESS_KEY"} {
		if seen[secret] {
			t.Errorf("%s reached a tool subprocess", secret)
		}
	}
}

// The tool contract is carried in the environment, so the filter cannot be a
// deny-list of things that look secret: what a tool is promised has to survive.
func TestAToolStillGetsWhatTheContractPromisesIt(t *testing.T) {
	a := newTestApp(t)
	installEnvTool(t, a, "envcheck")

	seen := toolEnv(t, a, "envcheck")
	for _, want := range []string{"AGENT_DB", "AGENT_DB_PREFIX", "AGENT_URL", "AGENT_WORKSPACE",
		"AGENT_SESSION", "AGENT_JOB", "TMPDIR", "PATH", "HOME"} {
		if !seen[want] {
			t.Errorf("%s did not reach the tool, and the contract says it will", want)
		}
	}
}

// A credential reaches a tool because the conversation it is running in was
// granted it. This is what lets one session push to a repository while every
// other conversation on the same agent cannot see the token at all.
func TestOnlyASessionGrantedACredentialReceivesIt(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp-example")
	a := newTestApp(t)
	installEnvTool(t, a, "envcheck")

	if !toolEnv(t, a, "envcheck", "GH_TOKEN")["GH_TOKEN"] {
		t.Error("a session granted a credential did not receive it")
	}
	if toolEnv(t, a, "envcheck")["GH_TOKEN"] {
		t.Error("a session granted nothing received the credential anyway")
	}
}

// A grant is a property of one conversation. Two sessions on the same agent,
// one granted and one not, must not be able to reach the same secret — that is
// the whole reason this moved off the manifest, where it was all of them or
// none of them, for the life of the deployment.
func TestAGrantDoesNotLeakIntoAnotherSession(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp-example")
	a := newTestApp(t)
	installEnvTool(t, a, "envcheck")
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tool did not load: %+v", failures)
	}

	granted, _ := a.NewSession(SessionConfig{Model: "test/model", GrantedEnv: []string{"GH_TOKEN"}}, "")
	plain, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")

	saw := func(id string) bool {
		res := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: id}, "envcheck", json.RawMessage(`{}`))
		if !res.OK {
			t.Fatalf("envcheck: %s", res.Error)
		}
		for _, v := range strings.Fields(res.Content) {
			if v == "GH_TOKEN" {
				return true
			}
		}
		return false
	}
	if !saw(granted.ID) {
		t.Error("the granted session did not get the credential")
	}
	if saw(plain.ID) {
		t.Error("a session that was granted nothing reached another session's credential")
	}
}

// Granting a variable that is not set must not put an empty one in its place: a
// tool checks whether it has a credential by asking whether it is there.
func TestAGrantedVariableThatIsUnsetIsAbsentRatherThanEmpty(t *testing.T) {
	os.Unsetenv("NOT_SET_ANYWHERE")
	a := newTestApp(t)
	installEnvTool(t, a, "envcheck")

	if toolEnv(t, a, "envcheck", "NOT_SET_ANYWHERE")["NOT_SET_ANYWHERE"] {
		t.Error("an unset variable was passed as an empty one")
	}
}

// The model key is the reason this allow-list exists at all, and a grant must
// not be a way to hand it back. It is the one name that cannot be granted, and
// granting it is refused rather than quietly dropped.
func TestTheModelKeyCannotBeGranted(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-should-not-be-visible")
	a := newTestApp(t)
	installEnvTool(t, a, "envcheck")

	if toolEnv(t, a, "envcheck", "OPENROUTER_API_KEY")["OPENROUTER_API_KEY"] {
		t.Error("the model key reached a tool because a session asked for it")
	}
}

// A session says which grants it holds, so what a conversation can reach is
// readable rather than inferred.
func TestASessionReportsTheGrantsItHolds(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model", GrantedEnv: []string{"GH_TOKEN"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	got := a.store.Session(s.ID)
	if got == nil {
		t.Fatal("the session is not there")
	}
	if len(got.GrantedEnv) != 1 || got.GrantedEnv[0] != "GH_TOKEN" {
		t.Errorf("granted_env = %v, want [GH_TOKEN]", got.GrantedEnv)
	}
}
