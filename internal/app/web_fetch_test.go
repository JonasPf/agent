package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strconv"
	"strings"
	"testing"
)

// web_fetch is the cheap half of the pair, and unlike web_browse it needs no
// browser — so these run everywhere, including the laptop where the browser
// tests skip. That is most of the point of having it.

func fetchURL(t *testing.T, a *App, url string) toolResult {
	t.Helper()
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	allowServer(t, a, url)
	args, _ := json.Marshal(map[string]string{"url": url})
	return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "web_fetch", args)
}

// A test server listens on a port nobody chose, and a confined tool connects
// only to the ports the sandbox names. The test names this one, the way an
// operator names theirs in AGENT_TOOL_PORTS.
func allowServer(t *testing.T, a *App, raw string) {
	t.Helper()
	u, err := neturl.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := strconv.Atoi(u.Port()); err == nil {
		a.sandbox.Ports = append(a.sandbox.Ports, n)
	}
}

func serve(t *testing.T, contentType, body string, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The ordinary case, and the one that makes the tool worth having: a page whose
// text is in the HTML the server sent, read without starting a browser.
func TestWebFetchReadsAPageTheServerAlreadyWrote(t *testing.T) {
	a := browseApp(t)
	url := serve(t, "text/html", `<html><body><h1>Widgets</h1>
<p>`+strings.Repeat("The widget is a small device that does a job. ", 20)+`</p></body></html>`, 200)

	res := fetchURL(t, a, url)
	if !res.OK {
		t.Fatalf("fetch failed: %s\n%s", res.Error, res.stderr)
	}
	if !strings.Contains(res.Content, "Widgets") {
		t.Errorf("the page's text did not come back: %q", res.Content)
	}
	if strings.Contains(res.Content, "web_browse") {
		t.Errorf("a page that was already readable was sent to a browser: %q", res.Content)
	}
}

// Fetching an API is a request for its bytes. Flattening them into prose the way
// an HTML document is flattened destroys the thing that was asked for.
func TestWebFetchReturnsAJSONBodyVerbatim(t *testing.T) {
	a := browseApp(t)
	body := `{"sku":"W-1","stock":4}`
	url := serve(t, "application/json", body, 200)

	res := fetchURL(t, a, url)
	if !res.OK {
		t.Fatalf("fetch failed: %s\n%s", res.Error, res.stderr)
	}
	if strings.TrimSpace(res.Content) != body {
		t.Errorf("content = %q, want the bytes as served", res.Content)
	}
}

// The property the old silent fallback did not have. A page whose text has not
// been written yet must say so and name the tool that can read it, because the
// alternative is a caller who cannot tell a shell from a short page.
func TestWebFetchSaysWhenThePageNeedsABrowser(t *testing.T) {
	a := browseApp(t)
	url := serve(t, "text/html", `<html><head><script src="/bundle.js"></script></head>
<body><div id="root"></div></body></html>`, 200)

	res := fetchURL(t, a, url)
	if !res.OK {
		t.Fatalf("fetch failed: %s\n%s", res.Error, res.stderr)
	}
	if !strings.Contains(res.Content, "web_browse") {
		t.Errorf("a page assembled by scripts did not name the tool that can read it: %q", res.Content)
	}
}

// A status code is part of the answer. A 404 page and a 500 page both carry text
// worth reading, and a caller that cannot see the code cannot tell a missing
// page from an empty one.
func TestWebFetchReportsARefusalRatherThanReturningItAsThePage(t *testing.T) {
	a := browseApp(t)
	url := serve(t, "text/html", `<html><body><p>No such item.</p></body></html>`, 404)

	res := fetchURL(t, a, url)
	if res.OK {
		t.Fatalf("a 404 came back as a successful read: %q", res.Content)
	}
	if !strings.Contains(res.Error, "404") {
		t.Errorf("the error does not name the status: %q", res.Error)
	}
	if !strings.Contains(res.Error, "No such item") {
		t.Errorf("the body the server sent was thrown away: %q", res.Error)
	}
}

// A fetch that failed is exactly when the caller needs to know the other tool
// exists. The note used to appear only on a page that came back readable, so a
// refusal, a 404, or a certificate the agent does not trust left the model with
// nothing to try next — and what it tried next was the same tool again.
func TestWebFetchNamesTheBrowserWhenTheFetchFails(t *testing.T) {
	a := browseApp(t)
	url := serve(t, "text/html", `<html><body><p>No such item.</p></body></html>`, 404)

	res := fetchURL(t, a, url)
	if res.OK {
		t.Fatalf("a 404 came back as a successful read: %q", res.Content)
	}
	if !strings.Contains(res.Error, "web_browse") {
		t.Errorf("a failed fetch did not name the tool that could still read the page: %q", res.Error)
	}
}

// The same holds when nothing answered at all: a connection that is refused,
// a name that does not resolve, a handshake the agent cannot complete.
func TestWebFetchNamesTheBrowserWhenNothingAnswers(t *testing.T) {
	a := browseApp(t)
	// A port nobody is listening on: the server is created and closed, so the
	// address is real and the connection is refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	res := fetchURL(t, a, url)
	if res.OK {
		t.Fatalf("a refused connection came back as a successful read: %q", res.Content)
	}
	if !strings.Contains(res.Error, "web_browse") {
		t.Errorf("a failed fetch did not name the tool that could still read the page: %q", res.Error)
	}
}
