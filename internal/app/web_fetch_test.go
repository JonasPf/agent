package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	args, _ := json.Marshal(map[string]string{"url": url})
	return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "web_fetch", args)
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
