// web_search puts a query to a search API and returns the hits. It used to
// drive a headless browser against a search engine's own results page; that
// engine now answers an automated client with a page of unrelated results
// rather than declining, and every other engine reachable from here refuses
// outright (ADR-059). An API answers the query it was asked.
package main

import (
	"os"
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

	// The key is the operator's and is set once for the agent. A model cannot
	// set it, so the message is addressed to the person who can, and says where
	// it goes rather than only that it is missing.
	key := strings.TrimSpace(os.Getenv(KeyName))
	if key == "" {
		tool.Failf("no search key: %s is not set, so this agent cannot search. "+
			"The operator sets it in the agent's .env and restarts; until then, fetch a page "+
			"you can name with web_fetch or web_browse.", KeyName)
	}

	results, err := Search(key, q, limit)
	if err != nil {
		tool.Failf("%v", err)
	}
	if len(results) == 0 {
		tool.OKf("no results for %q", q)
	}
	tool.OK(Format(results))
}
