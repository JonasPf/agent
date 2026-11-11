// read returns the contents of a file in the session's working directory.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// skillFile names the skill_read call for a path in the folder of a skill this
// conversation has, or returns "". A skill's scripts run from its folder, so the
// agent sees that path and reaches for read; told only that the path was
// outside the workspace, it found skill_read on its next call, in conversation
// after conversation. The agent says where those folders are with each call,
// in AGENT_SKILL_DIRS: skill name to folder.
func skillFile(path string) string {
	var dirs map[string]string
	if json.Unmarshal([]byte(os.Getenv("AGENT_SKILL_DIRS")), &dirs) != nil {
		return ""
	}
	full := filepath.Clean(path)
	if !filepath.IsAbs(full) {
		full = filepath.Join(tool.Workspace, full)
	}
	if real, err := filepath.EvalSymlinks(full); err == nil {
		full = real
	}
	for name, dir := range dirs {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		if rel, err := filepath.Rel(dir, full); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return fmt.Sprintf("it is a file of the skill %s: read it with skill_read {\"name\": %q, \"file\": %q}",
				name, name, filepath.ToSlash(rel))
		}
	}
	return ""
}

func main() {
	var a args
	tool.Args(&a)
	path, err := tool.ResolveInside(tool.Workspace, a.Path)
	if err != nil {
		if strings.TrimSpace(a.Path) == "" {
			tool.Failf("%s", err)
		}
		if hint := skillFile(a.Path); hint != "" {
			tool.Failf("%s; %s", err, hint)
		}
		tool.Failf("%s", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		tool.Failf("%s", err)
	}
	tool.OK(strings.Join(Window(strings.Split(string(b), "\n"), a.Offset, a.Limit), "\n"))
}
