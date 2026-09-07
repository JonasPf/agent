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

// browserOnThisMachine asks the tools' own helper, so the test and the tool
// agree on what counts as a browser rather than keeping two answers.
func browserOnThisMachine(t *testing.T) string {
	t.Helper()
	b, err := tool.Browser()
	if err != nil {
		t.Fatalf("could not resolve a browser: %v", err)
	}
	return b
}

func fetchApp(t *testing.T) *App {
	t.Helper()
	var readPaths string
	if b := browserOnThisMachine(t); b != "" {
		t.Setenv("AGENT_BROWSER", b)
		readPaths = filepath.Dir(b)
	}
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
	cfg := Config{DataDir: dir, Workspace: filepath.Join(dir, "workspace"),
		ToolsDir: "../../tools", DefaultModel: "test/model", ReadPaths: readPaths}
	a := &App{cfg: cfg, sandbox: NewSandbox(cfg), store: st,
		tools:  NewRegistry(cfg.ToolsDir, filepath.Join(dir, "agent.db"), st.DB()),
		skills: NewSkills(cfg.SkillsDir), hub: NewHub(),
		queues: map[string]chan func(){}, busy: map[string]bool{}}
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %v", failures)
	}
	return a
}

func fetchURL(t *testing.T, a *App, url string) toolResult {
	t.Helper()
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"url": url})
	return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "web_fetch", args)
}

// The tool as the registry runs it — a real subprocess, inside whatever sandbox
// this machine enforces, against a real server.
func TestWebFetchReadsAPageItsScriptsAssemble(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scriptedPage))
	}))
	defer srv.Close()

	a := fetchApp(t)
	res := fetchURL(t, a, srv.URL)
	if !res.OK {
		t.Fatalf("fetch failed: %s\n%s", res.Error, res.stderr)
	}

	if browserOnThisMachine(t) == "" {
		// A legitimate state: the tool falls back to plain HTTP and returns the
		// shell the server ships, which is the honest answer rather than an error.
		if !strings.Contains(res.Content, "Loading") {
			t.Errorf("no browser here, so the served page should come back as it is: %q", res.Content)
		}
		t.Log("no browser on this machine; the unrendered page above is expected")
		return
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
func TestWebFetchReturnsAJSONBodyVerbatim(t *testing.T) {
	body := `{"stock": 3, "name": "Widget & Co"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	res := fetchURL(t, fetchApp(t), srv.URL)
	if !res.OK {
		t.Fatalf("fetch failed: %s\n%s", res.Error, res.stderr)
	}
	if strings.TrimSpace(res.Content) != body {
		t.Errorf("content = %q, want the bytes as served %q", res.Content, body)
	}
}
