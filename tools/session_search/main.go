// session_search is full-text search over transcripts. It is what makes
// compaction a compression rather than a loss: what a fold takes out of the
// context it leaves in the index, so the agent can reach back for detail it can
// no longer see.
//
// A hit is a dozen words around the match, so it also reads a hit whole, by
// its number. Without that, an agent that lost its own last answer to a
// compaction spent four hours and 696 searches assembling it from snippets.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Query   string `json:"query"`
	Seq     *int   `json:"seq"`
	Session string `json:"session"`
	Limit   int    `json:"limit"`
}

type hit struct {
	SessionID string `json:"session_id"`
	Seq       int    `json:"seq"`
	Title     string `json:"title"`
	Snippet   string `json:"snippet"`
}

type call struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type entry struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Text       string          `json:"text"`
	ToolName   string          `json:"tool_name"`
	ToolCalls  []call          `json:"tool_calls"`
	ToolResult json.RawMessage `json:"tool_result"`
	CreatedAt  string          `json:"created_at"`
}

// readLimit caps an entry read whole. A tool result can be far longer than
// anything said, and the point is the words, not every byte a command printed.
const readLimit = 20000

func main() {
	var a args
	tool.Args(&a)
	// The default scope is this session. Searching one's own history is ordinary
	// and expected; reading another conversation is a thing the operator should be
	// able to see being decided, so widening is something the model has to ask
	// for — and asking appears in the transcript as the call it is.
	scope := strings.TrimSpace(a.Session)
	if a.Seq != nil {
		read(*a.Seq, scope)
	}
	q := strings.TrimSpace(a.Query)
	if q == "" {
		tool.Failf("query is required, or seq to read one entry whole")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 20
	}
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

// read answers with one entry, whole: who wrote it, when, and what it said,
// called, or returned.
func read(seq int, scope string) {
	if scope == "all" {
		tool.Failf("a number names an entry in one conversation; pass its session id, or omit session for this one")
	}
	params := url.Values{"seq": {strconv.Itoa(seq)}}
	if scope != "" {
		params.Set("session", scope)
	}
	var got struct {
		Entry     entry `json:"entry"`
		Reverted  bool  `json:"reverted"`
		Compacted bool  `json:"compacted"`
	}
	tool.API("GET", "/entry", nil, params, &got)
	e := got.Entry
	who := e.Role
	if who == "" {
		who = e.Type
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s, %s", e.Seq, who, e.CreatedAt)
	if got.Compacted {
		b.WriteString(" (folded away by a compaction; you no longer see it otherwise)")
	}
	if got.Reverted {
		b.WriteString(" (taken back by a revert)")
	}
	b.WriteString("\n\n")
	if e.Text != "" {
		b.WriteString(e.Text)
		b.WriteString("\n")
	}
	for _, c := range e.ToolCalls {
		fmt.Fprintf(&b, "called %s %s\n", c.Name, c.Arguments)
	}
	if len(e.ToolResult) > 0 {
		var r struct {
			Content string `json:"content"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(e.ToolResult, &r)
		fmt.Fprintf(&b, "%s returned: %s%s\n", e.ToolName, r.Content, r.Error)
	}
	out := b.String()
	if len(out) > readLimit {
		out = out[:readLimit] + fmt.Sprintf("\n[cut at %d characters]", readLimit)
	}
	tool.OK(out)
}
