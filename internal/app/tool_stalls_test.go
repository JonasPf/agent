package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A command that starts something in the background and returns — a server, a
// watcher — left the background process holding the shell's output open, and
// the call waited on it until its timeout: two minutes of a turn spent on a
// command that had finished at once.
func TestACommandThatLeavesAProcessRunningReturnsAtOnce(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	copyToolTree(t, "../../tools", toolsDir)
	a, _ := confinedApp(t, toolsDir, "", "")
	run := shellIn(t, a)

	start := time.Now()
	res := run("sleep 30 & echo started")
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the call waited %v on a process left in the background", took.Round(time.Second))
	}
	if !res.OK || !strings.Contains(res.Content, "started") {
		t.Errorf("result = %+v, want the command's own output", res)
	}
}

// A command that runs past its timeout is ended, and everything it started
// with it: the timeout bounds the whole call.
func TestACommandPastItsTimeoutEndsWithEverythingItStarted(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	copyToolTree(t, "../../tools", toolsDir)
	a, _ := confinedApp(t, toolsDir, "", "")
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "bash",
		[]byte(`{"command":"(sleep 60; echo late) & sleep 60","timeout_seconds":2}`))
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("a two-second timeout took %v", took.Round(time.Second))
	}
	if res.OK || !strings.Contains(res.Error, "timed out") {
		t.Errorf("result = %+v, want a timeout", res)
	}
}
