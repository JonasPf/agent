package app

import (
	"encoding/json"
	"fmt"
	"time"
)

// ChatMessage is the wire shape sent to the model.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []wireCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type wireCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Project converts a stored transcript into the message list for a model call.
//
//  1. Entries of type event are dropped.
//  2. The trailing run of job ticks sharing job_id and status collapses into one
//     line carrying the repetition count and the elapsed span.
//  3. Everything else passes through unchanged.
//
// Project is pure: same input, same output.
func Project(entries []Entry) []ChatMessage {
	// Find the trailing run of job ticks.
	end := len(entries)
	start := end
	var jobID, status string
	for i := end - 1; i >= 0; i-- {
		e := entries[i]
		if !isTick(e) {
			break
		}
		if start == end {
			jobID, status = e.JobID, e.Status
		} else if e.JobID != jobID || e.Status != status {
			break
		}
		start = i
	}

	var out []ChatMessage
	for i := 0; i < len(entries); i++ {
		if start < end && i == start {
			out = append(out, collapsed(entries[start:end]))
			break
		}
		e := entries[i]
		if e.Type == "event" {
			continue
		}
		if m, ok := toMessage(e); ok {
			out = append(out, m)
		}
	}
	return out
}

func isTick(e Entry) bool {
	return e.Type == "event" && e.EventKind == "job_check" && e.JobID != "" && e.Status != ""
}

func collapsed(run []Entry) ChatMessage {
	first, last := run[0], run[len(run)-1]
	span := last.CreatedAt.Sub(first.CreatedAt).Round(time.Second)
	verb := "condition not met"
	if last.Status == "fired" {
		verb = "condition met"
	}
	n := len(run)
	plural := "times"
	if n == 1 {
		plural = "time"
	}
	return ChatMessage{
		Role: "user",
		Content: fmt.Sprintf("[job %s checked %d %s over %s — %s. Last check %s.]",
			last.JobID, n, plural, span, verb, last.CreatedAt.Format(time.RFC3339)),
	}
}

func toMessage(e Entry) (ChatMessage, bool) {
	switch e.Role {
	case "user":
		return ChatMessage{Role: "user", Content: e.Text}, true
	case "assistant":
		m := ChatMessage{Role: "assistant"}
		if e.Text != "" {
			m.Content = e.Text
		}
		for _, tc := range e.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, wireCall{ID: tc.ID, Type: "function",
				Function: wireFunction{Name: tc.Name, Arguments: tc.Arguments}})
		}
		if m.Content == nil && len(m.ToolCalls) == 0 {
			return m, false
		}
		return m, true
	case "tool":
		body := string(e.ToolResult)
		if body == "" {
			body = e.Text
		}
		return ChatMessage{Role: "tool", ToolCallID: e.ToolCallID, Name: e.ToolName, Content: body}, true
	}
	return ChatMessage{}, false
}

func projectedTokens(entries []Entry) int {
	n := 0
	for _, m := range Project(entries) {
		b, _ := json.Marshal(m)
		n += estTokens(string(b))
	}
	return n
}
