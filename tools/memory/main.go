// memory writes and revises what the agent carries between sessions. Every item
// is visible at the top of the conversation, so nothing is remembered invisibly.
package main

import (
	"fmt"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Action string `json:"action"`
	ID     string `json:"id"`
	Text   string `json:"text"`
}

type item struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type page struct {
	Items    []item `json:"items"`
	Used     int    `json:"used"`
	Capacity int    `json:"capacity"`
}

func main() {
	var a args
	tool.Args(&a)
	switch a.Action {
	case "add":
		if strings.TrimSpace(a.Text) == "" {
			tool.Failf("text is required")
		}
		var m item
		tool.API("POST", "/memory",
			map[string]string{"text": a.Text, "source_session": tool.Session}, nil, &m)
		tool.OKf("remembered as %s", m.ID)
	case "edit":
		if a.ID == "" {
			tool.Failf("id is required")
		}
		tool.API("PATCH", "/memory/"+a.ID, map[string]string{"text": a.Text}, nil, nil)
		tool.OKf("updated %s", a.ID)
	case "delete":
		if a.ID == "" {
			tool.Failf("id is required")
		}
		tool.API("DELETE", "/memory/"+a.ID, nil, nil, nil)
		tool.OKf("deleted %s", a.ID)
	default:
		var p page
		tool.API("GET", "/memory", nil, nil, &p)
		if len(p.Items) == 0 {
			tool.OK("memory is empty")
		}
		lines := make([]string, 0, len(p.Items))
		for _, it := range p.Items {
			lines = append(lines, fmt.Sprintf("%s. %s", it.ID, it.Text))
		}
		tool.OKf("%s\n\n%d of %d tokens used", strings.Join(lines, "\n"), p.Used, p.Capacity)
	}
}
