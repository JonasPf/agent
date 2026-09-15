package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		notRun(t, "%v. This test is proved where the browser is installed — "+
			"in CI, and in the test container with `task check:container`.", err)
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
		ToolsDir: "../../tools"}
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

// A site reads the user agent before anything else, and headless chromium's
// says HeadlessChrome. The request carries the string of the Chrome that is
// actually running, with that word gone. It is checked at the server, because
// the server is the only reader whose opinion of it matters.
func TestWebBrowseIntroducesItselfAsTheChromeItIs(t *testing.T) {
	browserOrSkip(t)
	banner, err := exec.Command(tool.BrowserPath, "--version").Output()
	if err != nil {
		t.Fatalf("%s --version: %v", tool.BrowserPath, err)
	}
	major := regexp.MustCompile(`(\d+)\.\d+\.\d+\.\d+`).FindStringSubmatch(string(banner))
	if major == nil {
		t.Fatalf("no version in %q", banner)
	}
	agents := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case agents <- r.UserAgent():
		default:
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p>hello</p></body></html>`))
	}))
	defer srv.Close()

	res := browseURL(t, browseApp(t), srv.URL)
	if !res.OK {
		t.Fatalf("fetch failed: %s\n%s", res.Error, res.stderr)
	}
	select {
	case ua := <-agents:
		if strings.Contains(ua, "Headless") {
			t.Errorf("the request announced a headless browser: %q", ua)
		}
		if want := "Chrome/" + major[1] + ".0.0.0"; !strings.Contains(ua, want) {
			t.Errorf("user agent %q does not name the installed Chrome (%s)", ua, want)
		}
	default:
		t.Fatal("the server saw no request")
	}
}

// A bot check is served in place of the page, and a browser cannot tell: it
// loads, its scripts run, and it has text. The tool reads that text for what it
// is and fails naming whose check it was, so "Just a moment" never reaches the
// model as what a page says.
const botCheckPage = `<!DOCTYPE html><html><head><title>Just a moment...</title></head><body>
<h2>Performing security verification</h2>
<p>This website uses a security service to protect against malicious bots.</p>
<script>window._cf_chl_opt={cType:'managed'};</script>
<div>Performance and Security by Cloudflare</div></body></html>`

func TestWebBrowseReportsABotCheckRatherThanReadingIt(t *testing.T) {
	browserOrSkip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(botCheckPage))
	}))
	defer srv.Close()

	res := browseURL(t, browseApp(t), srv.URL)
	if res.OK {
		t.Fatalf("a bot check came back as the page: %q", res.Content)
	}
	if !strings.Contains(res.Error, "bot check") || !strings.Contains(res.Error, "Cloudflare") {
		t.Errorf("error %q does not say the page was Cloudflare's bot check", res.Error)
	}
}
