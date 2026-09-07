// read returns the contents of a file in the session's working directory.
package main

import (
	"os"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// Window applies offset and limit to a file's lines. Both are optional and
// either may be past the end, which is an empty answer rather than an error:
// reading past the end of a file is a fact about the file.
func Window(lines []string, offset, limit int) []string {
	if offset > 0 {
		if offset >= len(lines) {
			return nil
		}
		lines = lines[offset:]
	}
	if limit > 0 && limit < len(lines) {
		lines = lines[:limit]
	}
	return lines
}

func main() {
	var a args
	tool.Args(&a)
	path := tool.InsideWorkspace(a.Path)
	b, err := os.ReadFile(path)
	if err != nil {
		tool.Failf("%s", err)
	}
	tool.OK(strings.Join(Window(strings.Split(string(b), "\n"), a.Offset, a.Limit), "\n"))
}
