// web_search puts a query to a search engine in a headless browser and reads
// the results out of the rendered page — a results page is assembled by
// scripts, so fetching its HTML returns a shell with no results in it.
package main

import (
	"fmt"
	"net/url"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func main() {
	var a args
	tool.Args(&a)
	q := strings.TrimSpace(a.Query)
	if q == "" {
		tool.Failf("query is required")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 10
	}

	page := tool.Render("https://www.bing.com/search?q="+url.QueryEscape(q), 15)
	results := ParseResults(page)

	if len(results) == 0 {
		text := TextOf(page)
		if len(text) > 4000 {
			text = text[:4000]
		}
		if why := Refusal(text); why != "" {
			tool.Failf("the search engine declined this query (%s). Try again later, or "+
				"fetch a specific page with web_browse.", why)
		}
		tool.OKf("no results for %q", q)
	}

	// An engine that answers with somebody else's results has not answered, and
	// saying so is the whole point: the parsed output of a page of filler looks
	// exactly like the parsed output of a real one, so nothing downstream can
	// tell them apart. Checked before the limit, because every result on the
	// page is evidence.
	if Unrelated(q, results) {
		tool.Failf("the search engine returned %d results and not one of them mentions any part of %q. "+
			"It is serving this client unrelated filler rather than declining outright, so these are not an "+
			"answer to the query and are not shown. Fetch a page you can name with web_fetch or web_browse.",
			len(results), q)
	}

	if len(results) > limit {
		results = results[:limit]
	}
	var lines []string
	for i, r := range results {
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, r.Title, r.URL))
		if r.Snippet != "" {
			lines = append(lines, "   "+r.Snippet)
		}
	}
	tool.OK(strings.Join(lines, "\n"))
}
