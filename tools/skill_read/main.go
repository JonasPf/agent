// skill_read loads the full text of a skill. Only names and descriptions are in
// the prompt; this is what makes the rest reachable.
package main

import (
	"net/url"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Name string `json:"name"`
}

func main() {
	var a args
	tool.Args(&a)
	name := strings.TrimSpace(a.Name)
	if name == "" {
		tool.Failf("name is required")
	}
	var skill struct {
		Body string `json:"body"`
	}
	tool.API("GET", "/skills/"+url.PathEscape(name), nil, nil, &skill)
	if skill.Body == "" {
		tool.Failf("no skill named %q", name)
	}
	tool.OK(skill.Body)
}
