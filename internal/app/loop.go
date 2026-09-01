package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type turnOpts struct {
	UserText string
	JobID    string
	// Buffered holds a job run out of the transcript until it is known to have
	// fired. A run that does not fire is written as one event entry instead.
	Buffered bool
}

const maxToolRounds = 12

// runTurn assembles a request from the session, calls the model, dispatches tool
// calls, appends results, and repeats until the model stops calling tools.
func (a *App) runTurn(ctx context.Context, s *Session, opts turnOpts) (bool, error) {
	promptEntry, ok := a.promptEntry(s.ID)
	if !ok {
		return false, fmt.Errorf("session %s has no prompt entry", s.ID)
	}

	msgs := []ChatMessage{a.systemChatMessage(s, promptEntry.Sections)}
	msgs = append(msgs, Project(a.store.Entries(s.ID))...)

	user := Entry{Type: "message", Role: "user", Text: opts.UserText, JobID: opts.JobID}
	if opts.JobID != "" && !opts.Buffered {
		user.Status = "fired"
	}
	var buffer []Entry
	emit := func(e Entry) Entry {
		if opts.Buffered {
			e.Seq = -1
			e.CreatedAt = time.Now()
			buffer = append(buffer, e)
			a.hub.Broadcast(wsEvent{Kind: "transient", SessionID: s.ID, Entry: &e})
			return e
		}
		return a.append(s.ID, e)
	}
	emit(user)
	msgs = append(msgs, ChatMessage{Role: "user", Content: opts.UserText})

	fired := false
	tc := &ToolCtx{App: a, SessionID: s.ID, JobID: opts.JobID, Fired: &fired}

	for round := 0; round < maxToolRounds; round++ {
		req := ChatRequest{Model: s.Model, Messages: msgs, Tools: a.tools.SchemasFor(s)}
		a.hub.Broadcast(wsEvent{Kind: "turn_start", SessionID: s.ID})
		res, err := a.or.Chat(ctx, req, func(d string) {
			a.hub.Broadcast(wsEvent{Kind: "delta", SessionID: s.ID, Text: d})
		})
		a.hub.Broadcast(wsEvent{Kind: "turn_end", SessionID: s.ID})
		if err != nil {
			return fired, err
		}

		s.Cost += res.Usage.Cost
		s.PromptTokens += res.Usage.PromptTokens
		s.CachedTokens += res.Usage.CachedTokens
		_ = a.store.PutSession(s)

		usage := res.Usage
		assistant := Entry{Type: "message", Role: "assistant", Text: res.Text,
			ToolCalls: res.ToolCalls, JobID: opts.JobID, Usage: &usage}
		if opts.JobID != "" && !opts.Buffered {
			assistant.Status = "fired"
		}
		emit(assistant)

		if len(res.ToolCalls) == 0 {
			break
		}
		m := ChatMessage{Role: "assistant"}
		if res.Text != "" {
			m.Content = res.Text
		}
		for _, c := range res.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, wireCall{ID: c.ID, Type: "function",
				Function: wireFunction{Name: c.Name, Arguments: c.Arguments}})
		}
		msgs = append(msgs, m)

		for _, c := range res.ToolCalls {
			result := a.tools.Call(ctx, tc, c.Name, json.RawMessage(c.Arguments))
			body, _ := json.Marshal(result)
			emit(Entry{Type: "message", Role: "tool", ToolCallID: c.ID, ToolName: c.Name,
				ToolResult: body, Stderr: result.stderr, JobID: opts.JobID})
			msgs = append(msgs, ChatMessage{Role: "tool", ToolCallID: c.ID, Name: c.Name,
				Content: string(body)})
		}
	}

	if opts.Buffered {
		a.flushBuffered(s, opts.JobID, fired, buffer)
	}

	a.maybeSummarise(ctx, s)
	a.maybeRotate(s)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	return fired, nil
}

// flushBuffered writes a job run into the transcript. A run that fired becomes
// ordinary messages attributed to the job; a run that did not becomes one event
// entry with status not_fired, which is what lets the projection collapse it.
func (a *App) flushBuffered(s *Session, jobID string, fired bool, buffer []Entry) {
	if fired {
		for _, e := range buffer {
			e.Status = "fired"
			a.append(s.ID, e)
		}
		return
	}
	var said []string
	for _, e := range buffer {
		if e.Role == "assistant" && strings.TrimSpace(e.Text) != "" {
			said = append(said, strings.TrimSpace(e.Text))
		}
	}
	text := "checked, condition not met"
	if len(said) > 0 {
		text = truncate(strings.Join(said, " "), 600)
	}
	a.appendEvent(s.ID, Entry{EventKind: "job_check", JobID: jobID, Status: "not_fired", Text: text})
}

// systemChatMessage renders the fixed system prompt, adding a cache breakpoint
// only when another call is expected before the cache expires.
func (a *App) systemChatMessage(s *Session, sections []Section) ChatMessage {
	text := systemMessage(sections)
	ttl := a.cacheTTL(s)
	if ttl == "" || !needsExplicitCacheMarker(s.Model) {
		return ChatMessage{Role: "system", Content: text}
	}
	part := map[string]any{"type": "text", "text": text,
		"cache_control": map[string]any{"type": "ephemeral", "ttl": ttl}}
	return ChatMessage{Role: "system", Content: []any{part}}
}

// needsExplicitCacheMarker is false for providers that cache automatically.
func needsExplicitCacheMarker(model string) bool {
	return strings.HasPrefix(model, "anthropic/") || strings.HasPrefix(model, "google/")
}

// cacheTTL returns the cache lifetime to request, or "" for no cache write.
// A write costs more than a read, so it is requested only when a further call
// is expected within its lifetime.
func (a *App) cacheTTL(s *Session) string {
	if time.Since(a.lastUserTurn(s.ID)) < 5*time.Minute {
		return "5m"
	}
	jobs, err := a.store.SessionJobs(s.ID)
	if err != nil {
		return ""
	}
	for _, j := range jobs {
		if j.Check != "" {
			continue // a deterministic check makes no model call
		}
		if d := time.Until(j.NextRunAt); d > 0 && d < time.Hour {
			return "1h"
		}
	}
	return ""
}

func (a *App) lastUserTurn(sessionID string) time.Time {
	entries := a.store.Entries(sessionID)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "message" && entries[i].Role == "user" && entries[i].JobID == "" {
			return entries[i].CreatedAt
		}
	}
	return time.Time{}
}

// SendUserMessage queues a user turn behind anything already running.
func (a *App) SendUserMessage(sessionID, text string) error {
	s := a.LiveSession(sessionID)
	if s == nil {
		return fmt.Errorf("no session %s", sessionID)
	}
	a.enqueue(s.ID, func() {
		ctx := context.Background()
		live := a.store.Session(s.ID)
		if live == nil {
			return
		}
		first := a.lastUserTurn(live.ID).IsZero()
		if _, err := a.runTurn(ctx, live, turnOpts{UserText: text}); err != nil {
			a.appendEvent(live.ID, Entry{EventKind: "error", Text: "turn failed: " + err.Error()})
			a.Notify(Notification{Title: "Turn failed", Body: err.Error(), SessionID: live.ID})
			return
		}
		if first {
			a.titleIfNeeded(ctx, live, text)
		}
		live.Unread++
		_ = a.store.PutSession(live)
		a.hub.Broadcast(wsEvent{Kind: "sessions"})
	})
	return nil
}
