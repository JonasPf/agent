package app

import (
	"encoding/json"
	"fmt"
	"strings"
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
//  1. Entries of type event and the prompt entry are dropped.
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
			// A failure is the exception to dropping events. The turn it belongs
			// to left its input in the transcript with no answer, and without
			// this line the model cannot tell that from an instruction it was
			// given twice — so it answers it twice.
			if m, ok := failureMessage(e); ok {
				out = append(out, m)
			}
			continue
		}
		if e.Type == "prompt" {
			continue
		}
		if m, ok := toMessage(e); ok {
			out = append(out, m)
		}
	}
	return out
}

// wakeLateThreshold is how late a wake has to be before saying so is worth the
// words. The scheduler ticks once a second and a busy session defers a wake to
// the next tick, so a few seconds is the ordinary case, not a fact.
const wakeLateThreshold = time.Minute

// lateness describes the gap between when a wake was due and when it ran. A
// wake with no recorded due time says nothing: entries written before this was
// recorded must not start claiming to be punctual.
func lateness(e Entry) string {
	if e.DueAt.IsZero() || e.CreatedAt.IsZero() {
		return ""
	}
	late := e.CreatedAt.Sub(e.DueAt)
	if late < wakeLateThreshold {
		return ""
	}
	return fmt.Sprintf(" %s late, due %s", late.Round(time.Minute), e.DueAt.Format(time.RFC3339))
}

// failureMessage renders a run that produced nothing, in the same bracketed
// shape as a collapsed run of ticks: a line about the conversation rather than
// a turn in it.
func failureMessage(e Entry) (ChatMessage, bool) {
	if e.EventKind != "error" && e.EventKind != "job_error" {
		return ChatMessage{}, false
	}
	return ChatMessage{Role: "user",
		Content: fmt.Sprintf("[the turn above produced no reply — %s]", e.Text)}, true
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
		// A job wake and a typed message are both user turns, and without a
		// marker the model cannot tell them apart. It answered a reminder the
		// way it answers a person — in the transcript — while the operator was
		// not looking at it, so the wake says whose it is and that nobody is
		// there to read the reply.
		if e.JobID != "" {
			return ChatMessage{Role: "user",
				Content: fmt.Sprintf("[job %s woke this session%s; the operator is not present]\n%s",
					e.JobID, lateness(e), e.Text)}, true
		}
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

// messageText renders a projected message as plain text for the summariser.
// Content arrives as a string or, when a cache marker is attached, as parts;
// an assistant turn that only calls tools carries its substance in the calls.
func messageText(m ChatMessage) string {
	var sb strings.Builder
	switch c := m.Content.(type) {
	case string:
		sb.WriteString(c)
	case []any:
		for _, p := range c {
			if part, ok := p.(map[string]any); ok {
				if t, ok := part["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
	}
	for _, c := range m.ToolCalls {
		fmt.Fprintf(&sb, "\n[calls %s %s]", c.Function.Name, c.Function.Arguments)
	}
	return strings.TrimSpace(sb.String())
}
