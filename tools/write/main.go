// write creates or replaces a file in the session's working directory.
package main

import (
	"os"
	"path/filepath"

	"agent/internal/tool"
)

type args struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func main() {
	var a args
	tool.Args(&a)
	path := tool.InsideWorkspace(a.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tool.Failf("%s", err)
	}
	if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
		tool.Failf("%s", err)
	}
	tool.OKf("wrote %s (%d bytes)", a.Path, len(a.Content))
}
