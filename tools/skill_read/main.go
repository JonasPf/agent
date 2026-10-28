// skill_read loads the full text of a skill. Only names and descriptions are in
// the prompt; this is what makes the rest reachable. A skill that is a folder
// is read in two steps: SKILL.md, which names the other files, then any of them.
package main

import (
	"fmt"
	"net/url"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Name string `json:"name"`
	File string `json:"file"`
}

func main() {
	var a args
	tool.Args(&a)
	name := strings.TrimSpace(a.Name)
	if name == "" {
		tool.Failf("name is required")
	}
	if file := strings.Trim(strings.TrimSpace(a.File), "/"); file != "" {
		var f struct {
			Body string `json:"body"`
		}
		tool.API("GET", "/skills/"+url.PathEscape(name)+"/files/"+escapePath(file), nil, nil, &f)
		tool.OK(f.Body)
	}
	var skill struct {
		Body  string `json:"body"`
		Files []struct {
			Path  string `json:"path"`
			Bytes int    `json:"bytes"`
		} `json:"files"`
	}
	tool.API("GET", "/skills/"+url.PathEscape(name), nil, nil, &skill)
	if skill.Body == "" {
		tool.Failf("no skill named %q", name)
	}
	if len(skill.Files) == 0 {
		tool.OK(skill.Body)
	}
	// The other files are named after the text that refers to them, with the
	// call that reads one, so the model does not have to guess at either.
	var sb strings.Builder
	sb.WriteString(strings.TrimRight(skill.Body, "\n"))
	fmt.Fprintf(&sb, "\n\n---\nThis skill has more files. Read one with skill_read, its path as \"file\" — "+
		"for example {\"name\": %q, \"file\": %q}:\n", name, skill.Files[0].Path)
	for _, f := range skill.Files {
		fmt.Fprintf(&sb, "- %s (%d bytes)\n", f.Path, f.Bytes)
	}
	tool.OK(sb.String())
}

// escapePath escapes each part of a path and keeps the slashes between them.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
