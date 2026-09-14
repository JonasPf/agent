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
	// DueAt is when a job wake was scheduled for. It is not time.Now(): the two
	// differ whenever the host slept, the session was busy, or the run was
	// retried, and the difference is what tells the agent its reminder is stale.
	DueAt time.Time
}

const maxToolRounds = 12

// runTurn assembles a request from the session, calls the model, dispatches tool
// calls, appends results, and repeats until the model stops calling tools.
func (a *App) runTurn(ctx context.Context, s *Session, opts turnOpts) error {
	promptEntry, ok := a.promptEntry(s.ID)
	if !ok {
		return fmt.Errorf("session %s has no prompt entry", s.ID)
	}

	msgs := []ChatMessage{a.systemChatMessage(s, promptEntry.Sections)}
	msgs = append(msgs, Project(a.store.Entries(s.ID))...)
	if needsExplicitCacheMarker(s.Model) {
		msgs = withCacheMarker(msgs, a.cacheTTL(s))
	}

	user := Entry{Type: "message", Role: "user", Text: opts.UserText, JobID: opts.JobID,
		DueAt: opts.DueAt}
	if opts.JobID != "" {
		user.Status = "fired"
	}
	// Every entry is stored as it is produced. A run is never held back pending
	// an outcome: what the agent did is what the transcript says it did.
	emit := func(e Entry) Entry { return a.append(s.ID, e) }
	// The turn's own user message reaches the model through the projection, like
	// every earlier one. Building it here as well was how a job wake arrived
	// stripped of the marker that says the operator is not there to read a reply.
	if m, ok := toMessage(emit(user)); ok {
		msgs = append(msgs, m)
	}

	tc := &ToolCtx{App: a, SessionID: s.ID, JobID: opts.JobID}

	for round := 0; round < maxToolRounds; round++ {
		req := ChatRequest{Model: s.Model, Messages: msgs, Tools: a.tools.SchemasFor(s)}
		a.hub.Broadcast(wsEvent{Kind: "turn_start", SessionID: s.ID})
		res, err := a.or.Chat(ctx, req, func(d string) {
			a.hub.Broadcast(wsEvent{Kind: "delta", SessionID: s.ID, Text: d})
		})
		a.hub.Broadcast(wsEvent{Kind: "turn_end", SessionID: s.ID})
		if err != nil {
			return err
		}

		s.Cost += res.Usage.Cost
		s.PromptTokens += res.Usage.PromptTokens
		s.CachedTokens += res.Usage.CachedTokens
		_ = a.store.PutSession(s)

		usage := res.Usage
		assistant := Entry{Type: "message", Role: "assistant", Text: res.Text,
			ToolCalls: res.ToolCalls, JobID: opts.JobID, Usage: &usage}
		if opts.JobID != "" {
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

	a.maybeCompact(ctx, s)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	return nil
}

// systemChatMessage renders the prompt in force. It carries no cache marker:
// a marker names a prefix, so one at the end of the message list already covers
// the system prompt and everything after it.
func (a *App) systemChatMessage(s *Session, sections []Section) ChatMessage {
	return ChatMessage{Role: "system", Content: systemMessage(sections)}
}

// withCacheMarker puts one marker on the last message. A marker names a prefix —
// everything from the start of the request up to and including it — and a
// conversation only ever grows at the bottom, so each call reads the prefix the
// previous call wrote and writes one slightly longer. Only what was added since
// the last call is paid for in full.
//
// An empty ttl means the caller decided no further call is expected before the
// cache would expire, and a write nobody reads is the one way this loses money.
func withCacheMarker(msgs []ChatMessage, ttl string) []ChatMessage {
	if ttl == "" || len(msgs) == 0 {
		return msgs
	}
	out := make([]ChatMessage, len(msgs))
	copy(out, msgs)
	last := &out[len(out)-1]
	text, ok := last.Content.(string)
	if !ok {
		// Nothing to attach a marker to without changing what is sent. A message
		// carrying only tool calls is one of these, and skipping it costs a cache
		// write rather than correctness.
		return msgs
	}
	last.Content = []any{map[string]any{"type": "text", "text": text,
		"cache_control": map[string]any{"type": "ephemeral", "ttl": ttl}}}
	return out
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
	s := a.store.Session(sessionID)
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
		if err := a.runTurn(ctx, live, turnOpts{UserText: text}); err != nil {
			a.appendEvent(live.ID, Entry{EventKind: "error", Text: "turn failed: " + err.Error()})
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
