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
				"fetch a specific page with web_fetch.", why)
		}
		tool.OKf("no results for %q", q)
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
