// edit replaces a string in a file in the session's working directory.
package main

import (
	"fmt"
	"os"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Path       string `json:"path"`
	Old        string `json:"old"`
	New        string `json:"new"`
	ReplaceAll bool   `json:"replace_all"`
}

// Replace applies the edit, or says why it will not. An old string that matches
// more than once is refused unless the caller asked for every match: replacing
// the first of several is the edit that silently does the wrong thing.
func Replace(body, old, new string, all bool, name string) (string, int, error) {
	if old == "" {
		return "", 0, fmt.Errorf("old is required")
	}
	n := strings.Count(body, old)
	switch {
	case n == 0:
		return "", 0, fmt.Errorf("old string not found in %s", name)
	case n > 1 && !all:
		return "", 0, fmt.Errorf("old string appears %d times in %s; pass replace_all or give more context", n, name)
	case all:
		return strings.ReplaceAll(body, old, new), n, nil
	default:
		return strings.Replace(body, old, new, 1), n, nil
	}
}

func main() {
	var a args
	tool.Args(&a)
	path := tool.InsideWorkspace(a.Path)
	b, err := os.ReadFile(path)
	if err != nil {
		tool.Failf("%s", err)
	}
	out, n, err := Replace(string(b), a.Old, a.New, a.ReplaceAll, a.Path)
	if err != nil {
		tool.Failf("%s", err)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		tool.Failf("%s", err)
	}
	tool.OKf("edited %s (%d replacement(s))", a.Path, n)
}
