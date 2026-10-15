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

// runTurn assembles a request from the session, calls the model, dispatches tool
// calls, appends results, and repeats until the model stops calling tools.
func (a *App) runTurn(ctx context.Context, s *Session, opts turnOpts) error {
	promptEntry, ok := a.promptEntry(s.ID)
	if !ok {
		return fmt.Errorf("session %s has no prompt entry", s.ID)
	}

	msgs := []ChatMessage{a.systemChatMessage(s, promptEntry.Sections)}
	msgs = append(msgs, Project(a.store.Entries(s.ID))...)

	user := Entry{Type: "message", Role: "user", Text: opts.UserText, JobID: opts.JobID,
		DueAt: opts.DueAt}
	if opts.JobID != "" {
		user.Status = "fired"
	}
	// Every entry is stored as it is produced. A run is never held back pending
	// an outcome: what the agent did is what the transcript says it did.
	emit := func(e Entry) Entry { return a.append(s.ID, e) }
	stored := emit(user)

	// The gate asks whether a further call is expected while a cache would
	// live, so it is asked once this turn's own message is stored: an operator
	// who has just written is talking now. Asked before, the first message of
	// a conversation, and the first after a pause, wrote no marker, and every
	// round of that turn paid full price for what the round before had sent.
	ttl := ""
	if needsExplicitCacheMarker(s.Model) {
		ttl = a.cacheTTL(s)
		msgs = withCacheMarker(msgs, ttl)
	}
	marked := hasCacheMarker(msgs)

	// The turn's own user message reaches the model through the projection, like
	// every earlier one. Building it here as well was how a job wake arrived
	// stripped of the marker that says the operator is not there to read a reply.
	if m, ok := toMessage(stored); ok {
		msgs = append(msgs, m)
	}

	tc := &ToolCtx{App: a, SessionID: s.ID, JobID: opts.JobID}

	// A turn runs until the model stops calling tools. It was bounded at twelve
	// rounds, which a task of any size passes in a minute or two; the turn then
	// ended having written nothing, and the conversation went quiet with no
	// reason given. A runaway loop is the risk that bound was against, and it is
	// one to answer when it is seen, rather than by cutting every long task
	// short.
	for {
		req := ChatRequest{Model: s.Model, Messages: msgs, Tools: a.tools.SchemasFor(s)}
		a.hub.Broadcast(wsEvent{Kind: "turn_start", SessionID: s.ID})
		res, err := a.or.Chat(ctx, req, func(d string) {
			a.hub.Broadcast(wsEvent{Kind: "delta", SessionID: s.ID, Text: d})
		})
		a.hub.Broadcast(wsEvent{Kind: "turn_end", SessionID: s.ID})
		if err != nil {
			return err
		}

		usageNow := res.Usage
		ns, err := a.store.UpdateSession(s.ID, func(x *Session) {
			x.Cost += usageNow.Cost
			x.PromptTokens += usageNow.PromptTokens
			x.CachedTokens += usageNow.CachedTokens
			noteCache(x, ttl, marked, usageNow, time.Now())
		})
		if err != nil {
			// The session is gone; there is nobody to finish the turn for.
			return err
		}
		s = ns

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

// hasCacheMarker says whether a request carries the marker withCacheMarker
// writes. It is not written when there is nothing it could be attached to.
func hasCacheMarker(msgs []ChatMessage) bool {
	if len(msgs) == 0 {
		return false
	}
	parts, ok := msgs[len(msgs)-1].Content.([]any)
	if !ok {
		return false
	}
	for _, p := range parts {
		if m, ok := p.(map[string]any); ok && m["cache_control"] != nil {
			return true
		}
	}
	return false
}

// noteCache records what a call left the provider holding, for the status
// line's count of it. A call that carried the agent's marker wrote or read the
// prefix it names and restarted its lifetime; a provider that caches on its own
// holds what was sent, for as long as it chooses. A call to a provider that
// needs a marker and was sent none leaves whatever was held to lapse as it
// would have.
func noteCache(s *Session, ttl string, marked bool, u Usage, now time.Time) {
	if u.PromptTokens <= 0 {
		return
	}
	switch {
	case !needsExplicitCacheMarker(s.Model):
		s.Cache = &SessionCache{Tokens: u.PromptTokens, KeptBy: "provider"}
	case marked:
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return
		}
		s.Cache = &SessionCache{Tokens: u.PromptTokens, ExpiresAt: now.Add(d), KeptBy: "agent"}
	}
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

// stoppedWhere says where a stopped turn got to, from what it wrote before it
// was stopped. A turn stopped while it was still queued never reached the model,
// and "part way through" would be describing work that never began.
func stoppedWhere(written []Entry) string {
	for _, e := range written {
		if e.Type == "message" && e.Role != "user" {
			return "stopped, part way through"
		}
	}
	return "stopped before the agent began"
}

// SendUserMessage queues a user turn behind anything already running.
func (a *App) SendUserMessage(sessionID, text string) error {
	s := a.store.Session(sessionID)
	if s == nil {
		return fmt.Errorf("no session %s", sessionID)
	}
	a.enqueue(s.ID, func(ctx context.Context) {
		live := a.store.Session(s.ID)
		if live == nil {
			return
		}
		first := a.lastUserTurn(live.ID).IsZero()
		before := len(a.store.Entries(live.ID))
		if err := a.runTurn(ctx, live, turnOpts{UserText: text}); err != nil {
			// A turn the operator stopped did not fail: it ended where they
			// said. The transcript has to tell the two apart, or a stop reads
			// as the agent breaking.
			if ctx.Err() != nil {
				// A session deleted under its turn has nobody left to tell.
				if a.store.Session(live.ID) == nil {
					return
				}
				a.appendEvent(live.ID, Entry{EventKind: "cancelled",
					Text: stoppedWhere(a.entriesSince(live.ID, before))})
				a.hub.Broadcast(wsEvent{Kind: "sessions"})
				return
			}
			a.appendEvent(live.ID, Entry{EventKind: "error", Text: "turn failed: " + err.Error()})
			return
		}
		if first {
			a.titleIfNeeded(ctx, live, text)
		}
	})
	return nil
}
