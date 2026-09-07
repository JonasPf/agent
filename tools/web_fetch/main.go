// web_fetch loads a URL the way a person does — scripts run, the DOM settles —
// and returns the page's text.
package main

import (
	"io"
	"net/http"
	"os"
	"strings"

	"agent/internal/tool"
)

type args struct {
	URL string `json:"url"`
}

const (
	limit    = 40000
	maxBytes = 2 << 20
)

// overHTTP fetches the URL's bytes without a browser: no scripts run, so a page
// that assembles itself on the client comes back as the shell it ships as.
func overHTTP(url string) string {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		tool.Failf("%s", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; agent/0.1)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tool.Failf("%s", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		tool.Failf("%s", err)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "html") {
		return string(b)
	}
	return ToText(string(b))
}

// inBrowser runs the page and reads the settled DOM. The browser is used as it
// comes; nothing here disguises the request or works around a site that
// declines to serve it.
func inBrowser(url string) string {
	dom := tool.Render(url, 15)
	if body, ok := UnwrapPlain(dom); ok {
		return body
	}
	return ToText(dom)
}

func main() {
	var a args
	tool.Args(&a)
	url := strings.TrimSpace(a.URL)
	if url == "" {
		tool.Failf("url is required")
	}

	browser := ""
	if os.Getenv("AGENT_NO_BROWSER") == "" {
		b, err := tool.Browser()
		if err != nil {
			tool.Failf("%s", err)
		}
		browser = b
	}
	// Without a browser the tool still fetches. A page that needs scripts comes
	// back thin, which is the honest result rather than an error.
	var out string
	if browser != "" {
		out = inBrowser(url)
	} else {
		out = overHTTP(url)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	tool.OK(out)
}
