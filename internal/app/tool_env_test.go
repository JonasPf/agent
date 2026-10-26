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
		"name": name, "description": "Report the environment.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
		"agent":      map[string]any{"db_prefix": name + "_"},
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

// toolEnv runs the named tool in a new session, and returns the variable
// names the subprocess actually saw.
func toolEnv(t *testing.T, a *App, name string) map[string]bool {
	t.Helper()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tool did not load: %+v", failures)
	}
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
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

// Nothing reads the agent's own environment on a conversation's behalf any
// more: a variable the agent was started with reaches no tool unless a manifest
// names it, whatever any conversation holds.
func TestTheAgentsOwnEnvironmentReachesNoToolThroughAConversation(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp-from-the-deployment")
	a := newTestApp(t)
	installEnvTool(t, a, "envcheck")
	if toolEnv(t, a, "envcheck")["GH_TOKEN"] {
		t.Error("a variable from the agent's own environment reached a tool")
	}
}
