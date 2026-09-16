// web_fetch retrieves what a server sends and returns it as text. No browser,
// no scripts: one request, and whatever came back.
//
// It is the cheap half of a pair. web_browse runs the page in a browser, which
// answers correctly for everything and costs a hundred times as much; this
// answers correctly for an API, a document, a feed, a README — most of what is
// actually asked for — in a few milliseconds.
//
// What makes the pair safe is that this one can tell when it was the wrong
// choice. Its predecessor was a silent fallback inside the browser tool: a page
// whose text had not been written yet came back as the shell it ships as, and
// whoever asked could not tell that from a page that says little. So the shape
// is the same and the honesty is new — the result says when the page looks
// unfinished, and the caller decides whether to spend a browser on it.
package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"agent/internal/tool"
)

type args struct {
	URL string `json:"url"`
}

const (
	limit    = 40000
	maxBytes = 2 << 20
	// Long enough for a slow server, short enough that a hung one does not eat
	// the tool's whole timeout and leave nothing to report.
	wait = 20 * time.Second
)

// tryBrowse is appended to a body that looks unfinished. It names the tool
// rather than describing it, because the reader is choosing what to call next.
const tryBrowse = "\n\n---\n[web_fetch] This page's text appears to be written by its scripts, " +
	"which did not run here — what is above may be only the shell the server ships. " +
	"Call web_browse on the same URL to read the page as a person would see it."

// afterFailure is appended to an error. A fetch that failed is the moment the
// caller most needs to know the other tool exists: the note above appears only
// on a page that came back readable, so a refusal, a 404, or a handshake that
// failed left the model with nothing to try next — and what it tried next was
// this tool again, on another URL, until it ran out of guesses.
//
// It says when to stop, too. A browser is one more attempt, not an unlimited
// one, and a page neither tool can reach is a page to find another way.
const afterFailure = "\n\n[web_fetch] A browser may still reach it: the page may be served " +
	"only to one, or assembled by scripts this request did not run. Call web_browse on the " +
	"same URL. If that fails too, this page is out of reach — look for the information " +
	"elsewhere rather than fetching this URL again."

func main() {
	var a args
	tool.Args(&a)
	url := strings.TrimSpace(a.URL)
	if url == "" {
		tool.Failf("url is required")
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		tool.Failf("%s", err)
	}
	// The request is not disguised. Nothing here pretends to be a browser it is
	// not, and a site that declines to serve the agent is reported as declining.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; agent/0.1)")
	client := &http.Client{Timeout: wait}
	resp, err := client.Do(req)
	if err != nil {
		tool.Failf("%s%s", err, afterFailure)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		tool.Failf("%s%s", err, afterFailure)
	}
	// A status is part of the answer, not a reason to say nothing: a 404 page and
	// a 500 page both carry text worth reading, and hiding the code behind them
	// would leave a caller unable to tell a missing page from an empty one.
	if resp.StatusCode >= 400 {
		tool.Failf("%s returned %s. The body it sent:\n\n%s%s",
			url, resp.Status, truncate(bodyText(resp, string(b)), 2000), afterFailure)
	}

	out := bodyText(resp, string(b))
	// Only a document can be waiting on scripts; a JSON body is the answer.
	if isDocument(resp) && NeedsScripts(string(b)) {
		out += tryBrowse
	}
	tool.OK(truncate(out, limit))
}

// bodyText flattens a document and leaves anything else exactly as it arrived:
// the point of fetching an API is the bytes it returned.
func bodyText(resp *http.Response, body string) string {
	if !isDocument(resp) {
		return body
	}
	return tool.ToText(body)
}

func isDocument(resp *http.Response) bool {
	return strings.Contains(resp.Header.Get("Content-Type"), "html")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("\n\n[web_fetch] …truncated at %d characters]", n)
}
