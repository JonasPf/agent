// session_search is full-text search over transcripts. It is what makes
// compaction a compression rather than a loss: what a fold takes out of the
// context it leaves in the index, so the agent can reach back for detail it can
// no longer see.
package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Query   string `json:"query"`
	Session string `json:"session"`
	Limit   int    `json:"limit"`
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
	// The default scope is this session. Searching one's own history is ordinary
	// and expected; reading another conversation is a thing the operator should be
	// able to see being decided, so widening is something the model has to ask
	// for — and asking appears in the transcript as the call it is.
	scope := strings.TrimSpace(a.Session)
	if scope == "" {
		scope = tool.Session
	}
	if scope == "all" {
		scope = ""
	}
	params := url.Values{"q": {q}, "limit": {strconv.Itoa(limit)}}
	if scope != "" {
		params.Set("session", scope)
	}
	var hits []hit
	tool.API("GET", "/search", nil, params, &hits)
	if len(hits) == 0 {
		if scope != "" {
			tool.OKf("no matches in this session for %q. Pass session:\"all\" to search every conversation.", q)
		}
		tool.OKf("no matches for %q in any session", q)
	}
	lines := make([]string, 0, len(hits))
	for _, h := range hits {
		lines = append(lines, fmt.Sprintf("%s #%d (%s): %s", h.SessionID, h.Seq, h.Title, h.Snippet))
	}
	tool.OK(strings.Join(lines, "\n"))
}
