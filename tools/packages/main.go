// packages finds and installs a program the image does not ship, from Nix's
// stable release (ADR-072). The agent does the install, into a store every
// conversation reads and none writes; this hands it the names and reports what
// it did.
package main

import (
	"net/url"
	"time"

	"agent/internal/tool"
)

type args struct {
	Search  string   `json:"search,omitempty"`
	Install []string `json:"install,omitempty"`
}

func main() {
	var a args
	tool.Args(&a)
	var res struct {
		Content string `json:"content"`
	}
	switch {
	case a.Search != "" && len(a.Install) > 0:
		tool.Failf("search or install, not both: search first, then install a name the search listed")
	case a.Search != "":
		// The first search of the day reads the release's list afresh.
		tool.SetAPITimeout(5 * time.Minute)
		tool.API("GET", "/packages", nil, url.Values{"search": {a.Search}}, &res)
	case len(a.Install) > 0:
		// Installs wait their turn, and a large one downloads for minutes; the
		// manifest's timeout is the bound.
		tool.SetAPITimeout(25 * time.Minute)
		tool.API("POST", "/packages", map[string]any{"install": a.Install}, nil, &res)
	default:
		tool.API("GET", "/packages", nil, nil, &res)
	}
	tool.OK(res.Content)
}
