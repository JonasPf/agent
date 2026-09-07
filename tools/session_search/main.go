// session_search is full-text search across every transcript, so a conversation
// that rotated away is still reachable by what was said in it.
package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type hit struct {
	SessionID string `json:"session_id"`
	Seq       int    `json:"seq"`
	Title     string `json:"title"`
	Snippet   string `json:"snippet"`
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
		limit = 20
	}
	var hits []hit
	tool.API("GET", "/search", nil,
		url.Values{"q": {q}, "limit": {strconv.Itoa(limit)}}, &hits)
	if len(hits) == 0 {
		tool.OK("no matches")
	}
	lines := make([]string, 0, len(hits))
	for _, h := range hits {
		lines = append(lines, fmt.Sprintf("%s #%d (%s): %s", h.SessionID, h.Seq, h.Title, h.Snippet))
	}
	tool.OK(strings.Join(lines, "\n"))
}
