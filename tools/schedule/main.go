// schedule creates, lists, edits, and deletes jobs — the rows that make future
// work visible before it happens.
package main

import (
	"fmt"
	"net/url"
	"strings"

	"agent/internal/tool"
)

type args struct {
	Action      string `json:"action"`
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	Schedule    string `json:"schedule"`
	Check       string `json:"check"`
	Prompt      string `json:"prompt"`
	AfterActing string `json:"after_acting"`
}

func main() {
	var a args
	tool.Args(&a)
	switch a.Action {
	case "create":
		body := map[string]string{
			"schedule": a.Schedule, "check": a.Check, "prompt": a.Prompt,
			"after_acting": a.AfterActing, "session_id": a.SessionID,
		}
		if body["session_id"] == "" {
			body["session_id"] = tool.Session
		}
		var j Job
		tool.API("POST", "/jobs", body, nil, &j)
		tool.OKf("job %s created · wakes %s · %s after acting%s",
			j.ID, j.NextRunAt, j.AfterActing, Cost(j))
	case "delete":
		id := a.ID
		if id == "" {
			id = tool.Job
		}
		if id == "" {
			tool.Failf("id is required")
		}
		tool.API("DELETE", "/jobs/"+id, nil, nil, nil)
		tool.OKf("job %s deleted", id)
	case "edit":
		if a.ID == "" {
			tool.Failf("id is required")
		}
		patch := map[string]string{}
		for k, v := range map[string]string{"schedule": a.Schedule, "check": a.Check,
			"prompt": a.Prompt, "after_acting": a.AfterActing} {
			if v != "" {
				patch[k] = v
			}
		}
		tool.API("PATCH", "/jobs/"+a.ID, patch, nil, nil)
		tool.OKf("job %s updated", a.ID)
	default:
		var jobs []Job
		tool.API("GET", "/jobs", nil, url.Values{"session_id": {tool.Session}}, &jobs)
		if len(jobs) == 0 {
			tool.OK("no jobs in this session")
		}
		lines := make([]string, 0, len(jobs))
		for _, j := range jobs {
			lines = append(lines, Line(j))
		}
		tool.OK(strings.Join(lines, "\n"))
	}
}

// Line is one job as the operator reads it in a list.
func Line(j Job) string {
	status := j.Status
	if status == "" {
		status = "scheduled"
	}
	check := j.Check
	if check == "" {
		check = "(none)"
	}
	next := j.NextRunAt
	if status == "done" {
		next = "(none)"
	}
	prompt := j.Prompt
	if len(prompt) > 100 {
		prompt = prompt[:100]
	}
	return fmt.Sprintf("%s · %s · schedule %s · check %s · %s after acting · %d runs · next wake %s%s · %s",
		j.ID, status, j.Schedule, check, j.AfterActing, j.RunCount, next, Cost(j), prompt)
}
