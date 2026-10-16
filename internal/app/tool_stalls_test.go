package app

import (
	"os"
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

// A process left in the background outlives the shell that started it, and
// whoever inherits it has to collect it when it exits. Nothing did: it stayed a
// zombie, and `kill -0` on a zombie succeeds, so a session that polled "is it
// still running?" heard yes every two minutes for three hours about a script
// that had finished in five.
func TestABackgroundProcessThatExitsIsGone(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	copyToolTree(t, "../../tools", toolsDir)
	a, _ := confinedApp(t, toolsDir, "", "")
	run := shellIn(t, a)

	res := run("sleep 2 >/dev/null 2>&1 & echo $!")
	pid := strings.TrimSpace(res.Content)
	if !res.OK || pid == "" {
		t.Fatalf("result = %+v, want the background process's pid", res)
	}
	alive := "kill -0 " + pid + " 2>/dev/null && echo running || echo gone"
	if got := run(alive); !strings.Contains(got.Content, "running") {
		t.Fatalf("while it runs, the check says %+v; it cannot tell running from gone", got)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		got := run(alive)
		if strings.Contains(got.Content, "gone") {
			return
		}
		if time.Now().After(deadline) {
			stat, _ := os.ReadFile("/proc/" + pid + "/stat")
			t.Fatalf("pid %s still answers 15s after it exited; /proc says %q", pid, stat)
		}
		time.Sleep(500 * time.Millisecond)
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
