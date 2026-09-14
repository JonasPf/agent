package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent/internal/tool"
)

// Most of the web arrives empty: the HTML a server sends is a shell, and the
// text a person reads is written into it by scripts once the page is running.
// A fetch that reads the bytes therefore answers a different question from the
// one that was asked, and answers it wrongly without saying so.
const scriptedPage = `<html><head><title>Shop</title></head><body>
<div id="out">Loading…</div>
<script>document.getElementById('out').textContent = 'Widget in stock';</script>
</body></html>`

// browserOrSkip states what this machine can prove. The browser is installed by
// the image and by CI, at one path; a laptop has neither it nor a sandbox, and
// the answer to both is the same — run the container. A test that cannot reach
// a browser says so rather than passing against a tool that never ran one.
func browserOrSkip(t *testing.T) {
	t.Helper()
	if _, err := tool.Browser(); err != nil {
		t.Skipf("NOT RUN: %v. This test is proved where the browser is installed — "+
			"in CI, and in the container with `task dev`.", err)
	}
}

func browseApp(t *testing.T) *App {
	t.Helper()
	// Not t.TempDir(): it names the directory after the test, and a browser
	// opens a UNIX socket inside the session's scratch directory, where the
	// whole path must fit in 104 bytes. The deployed workspace is /app/workspace
	// and nowhere near it; a test name is.
	dir, err := os.MkdirTemp("", "wf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// No ReadPaths: the browser lives under /usr, which every tool may already
	// read. Needing to name it here would mean the deployment needed to as well.
	cfg := Config{DataDir: dir, Workspace: filepath.Join(dir, "workspace"),
		ToolsDir: "../../tools", DefaultModel: "test/model"}
	a := &App{cfg: cfg, sandbox: NewSandbox(cfg), store: st,
		tools:  NewRegistry(cfg.ToolsDir, DBPath(dir), st.DB()),
		skills: NewSkills(cfg.SkillsDir), hub: NewHub(),
		queues: map[string]chan func(){}, busy: map[string]bool{}}
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %v", failures)
	}
	return a
}

func browseURL(t *testing.T, a *App, url string) toolResult {
	t.Helper()
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"url": url})
	return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
}

// The tool as the registry runs it — a real subprocess, inside whatever sandbox
// this machine enforces, against a real server.
func TestWebBrowseReadsAPageItsScriptsAssemble(t *testing.T) {
	browserOrSkip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scriptedPage))
	}))
	defer srv.Close()

	a := browseApp(t)
	res := browseURL(t, a, srv.URL)
	if !res.OK {
		t.Fatalf("fetch failed: %s\n%s", res.Error, res.stderr)
	}

	if !strings.Contains(res.Content, "Widget in stock") {
		t.Errorf("the page's scripts did not run: %q", res.Content)
	}
	if strings.Contains(res.Content, "Loading") {
		t.Errorf("the placeholder survived, so the DOM was read before it settled: %q", res.Content)
	}
}

// Fetching an API is a request for its bytes. Flattening them into prose the
// way an HTML document is flattened destroys the thing that was asked for.
func TestWebBrowseReturnsAJSONBodyVerbatim(t *testing.T) {
	browserOrSkip(t)
	body := `{"stock": 3, "name": "Widget & Co"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	res := browseURL(t, browseApp(t), srv.URL)
	if !res.OK {
		t.Fatalf("fetch failed: %s\n%s", res.Error, res.stderr)
	}
	if strings.TrimSpace(res.Content) != body {
		t.Errorf("content = %q, want the bytes as served %q", res.Content, body)
	}
}
