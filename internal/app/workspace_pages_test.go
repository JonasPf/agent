package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pages in a working directory: what the agent wrote or fetched is shown,
// never trusted.

// A file in a working directory is whatever the agent wrote or fetched, so it
// is shown, never trusted: a page opened from one runs with no origin, and no
// way to reach the routes the operator's login opens.
func TestAPageInAWorkingDirectoryOpensWithoutTheOperatorsAuthority(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	for _, name := range []string{"page.html", "drawing.svg", "no-extension"} {
		if w := upload(t, a, s.ID, name, "<html><script>fetch('/skills')</script></html>"); w.Code != 201 {
			t.Fatalf("upload %s = %d", name, w.Code)
		}
		h := get(t, a, "/sessions/"+s.ID+"/files/"+name, false).Header()
		csp := h.Get("Content-Security-Policy")
		if !strings.Contains(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") {
			t.Errorf("%s: Content-Security-Policy = %q, want a sandbox without the agent's origin", name, csp)
		}
		if !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: Content-Security-Policy = %q, want nothing loaded from anywhere", name, csp)
		}
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", name, got)
		}
	}
}

// A browser refuses to show a PDF in a sandbox, and shows one without running
// it on this origin, so a PDF keeps opening.
func TestAPDFInAWorkingDirectoryStillOpens(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	if w := upload(t, a, s.ID, "report.pdf", "%PDF-1.7"); w.Code != 201 {
		t.Fatalf("upload = %d", w.Code)
	}
	h := get(t, a, "/sessions/"+s.ID+"/files/report.pdf", false).Header()
	if got := h.Get("Content-Security-Policy"); got != "" {
		t.Errorf("a PDF was sent with Content-Security-Policy %q", got)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
}

// A page opened straight from the Files screen runs its scripts, and reaches
// nothing.
func TestAPageOpenedFromFilesRunsAndCannotReachTheAgent(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	s := newSession(t, a)
	page := `<!doctype html><html><body><p id="out">not run</p><script>
		fetch('` + srv.URL + `/skills', {method: 'POST', mode: 'no-cors', body: '{}'}).then(
			function () { document.getElementById('out').textContent = 'reached the agent'; },
			function () { document.getElementById('out').textContent = 'stayed in the box'; });
	</script></body></html>`
	if err := os.WriteFile(filepath.Join(a.ensureWorkspace(s.ID), "mock.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/sessions/" + s.ID + "/files/mock.html"})
	shown := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !shown.OK {
		t.Fatalf("web_browse: %s", shown.Error)
	}
	if !strings.Contains(shown.Content, "stayed in the box") {
		t.Errorf("the page did not run, or reached the agent:\n%s", shown.Content)
	}
}
