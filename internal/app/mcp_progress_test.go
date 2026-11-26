package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// longManifest is the stand-in server as a bundle whose tools take time
// (testenv/mcp/shell, "long").
const longManifest = `{
  "manifest_version": "0.3",
  "name": "long",
  "version": "0.1.0",
  "description": "Tools that take time, for the tests.",
  "author": {"name": "the tests"},
  "server": {
    "type": "binary",
    "entry_point": "server/shell",
    "mcp_config": {"command": "${__dirname}/server/shell", "args": ["long"]}
  },
  "compatibility": {"platforms": ["linux"]},
  "_meta": {"io.github.jonaspf.agent": {"timeout_seconds": 20}}
}`

func longApp(t *testing.T) *App {
	t.Helper()
	bundles := t.TempDir()
	writeBundle(t, bundles, "long", longManifest)
	a, failures := bundleApp(t, bundles)
	if len(failures) > 0 {
		t.Fatalf("the bundle did not load: %+v", failures)
	}
	return a
}

// "While a bundle's tool runs, what its server says of how far it has got
// reaches the conversation's screen, the latest each time."
func TestABundleToolsProgressReachesTheScreen(t *testing.T) {
	a := longApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	events := a.hub.add()
	defer a.hub.remove(events)
	res := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "steps", json.RawMessage(`{}`))
	if !res.OK || !strings.Contains(res.Content, "token given: true") {
		t.Fatalf("steps = %+v, want it done, and told where to say how far it got", res)
	}
	// A notification is read as it arrives, and the last may be handed on just
	// after the result.
	var said []string
	for wait := time.After(2 * time.Second); len(said) < 3; {
		select {
		case e := <-events:
			if e.Kind == "progress" && e.SessionID == s.ID {
				said = append(said, e.Text)
			}
		case <-wait:
			t.Fatalf("progress shown = %q, want three", said)
		}
	}
	want := []string{"steps: step 1 (1 of 3)", "steps: step 2 (2 of 3)", "steps: step 3 (3 of 3)"}
	if strings.Join(said, "|") != strings.Join(want, "|") {
		t.Errorf("progress shown = %q, want %q", said, want)
	}
}

// "Stopping a turn tells the server of a running call that it is cancelled,
// and gives it a moment to stop before it is ended."
func TestStoppingATurnTellsTheServerItsCallIsCancelled(t *testing.T) {
	a := longApp(t)
	srv := scriptedModel(t, toolStep{tool: "wait", args: "{}"}, toolStep{text: "Not reached."})
	a.or.base = srv.URL
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err := a.SendUserMessage(s.ID, "wait"); err != nil {
		t.Fatal(err)
	}
	dir := a.ensureWorkspace(s.ID)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "waiting.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tool never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if w := callAPI(t, a, "POST", "/sessions/"+s.ID+"/cancel", nil); w.Code != http.StatusOK && w.Code != http.StatusAccepted && w.Code != http.StatusNoContent {
		t.Fatalf("stopping = %d %s", w.Code, w.Body)
	}
	settle(t, a, s.ID)
	if _, err := os.Stat(filepath.Join(dir, "cancelled.txt")); err != nil {
		t.Errorf("the server was ended without being told its call was cancelled: %v", err)
	}
}
