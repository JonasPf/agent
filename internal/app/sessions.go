package app

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// baseConfig is the configuration a new session starts from when the request
// does not fully specify one: the seed's, else the most recently used session's,
// else the configured default.
func (a *App) baseConfig(seed *Session) SessionConfig {
	if seed == nil {
		if recent := a.store.Sessions(); len(recent) > 0 {
			seed = recent[0]
		}
	}
	if seed != nil {
		return seed.SessionConfig
	}
	return SessionConfig{Model: a.cfg.DefaultModel}
}

// NewSession creates a session under an already resolved configuration and
// writes its prompt entry, which is the complete system prompt as sent and is
// fixed for the life of the session.
func (a *App) NewSession(cfg SessionConfig, continuedFrom string) (*Session, error) {
	if cfg.Model == "" {
		cfg.Model = a.cfg.DefaultModel
	}
	now := time.Now()
	s := &Session{
		ID:              newID(),
		Title:           "New session",
		SessionConfig:   cfg,
		Status:          "active",
		ContinuedFrom:   continuedFrom,
		RotateAtTokens:  a.cfg.RotateAtTokens,
		CarryOverTokens: a.cfg.CarryOverTokens,
		CreatedAt:       now,
		LastActiveAt:    now,
	}
	if err := a.store.PutSession(s); err != nil {
		return nil, err
	}
	sections := a.systemSections(s)
	a.append(s.ID, Entry{Type: "event", EventKind: "prompt", Sections: sections,
		Text: fmt.Sprintf("system prompt · %d tokens", sectionsTotal(sections))})
	return s, nil
}

// Rotate seeds a successor from a predecessor: summary first, then the most
// recent complete turns that fit the carry-over budget. Rotation, fork, resume,
// and reconfiguration are the same operation — a session's configuration is
// fixed, so changing it is exactly the act of continuing in a new one.
func (a *App) Rotate(pred *Session, cfg SessionConfig, archive bool, why string) (*Session, error) {
	succ, err := a.NewSession(cfg, pred.ID)
	if err != nil {
		return nil, err
	}
	if changed := describeConfigChange(pred.SessionConfig, succ.SessionConfig); changed != "" {
		why += ", " + changed
	}
	carried := carryOver(a.store.Entries(pred.ID), pred.CarryOverTokens)
	a.append(succ.ID, Entry{Type: "event", EventKind: "carried_over", CarriedFrom: pred.ID,
		Text: fmt.Sprintf("seeded from %s (%s): summary and %d carried messages", pred.ID, why, len(carried))})
	if pred.Summary != "" {
		a.append(succ.ID, Entry{Type: "message", Role: "user", CarriedFrom: pred.ID,
			Text: "[summary of " + pred.ID + "]\n" + pred.Summary})
	}
	for _, e := range carried {
		e.CarriedFrom = pred.ID
		e.Usage = nil
		a.append(succ.ID, e)
	}

	pred.ContinuedBy = succ.ID
	if archive {
		pred.Status = "archived"
	}
	_ = a.store.PutSession(pred)
	if archive {
		if err := a.store.MoveJobs(pred.ID, succ.ID); err != nil {
			return nil, err
		}
	}
	a.append(pred.ID, Entry{Type: "event", EventKind: "rotation",
		Text: fmt.Sprintf("continued in %s (%s)", succ.ID, why)})
	succ.Title = pred.Title
	_ = a.store.PutSession(succ)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	return succ, nil
}

// carryOver returns the trailing complete turns that fit a token budget, oldest
// first. A turn is carried whole or not at all.
func carryOver(entries []Entry, budget int) []Entry {
	var msgs []Entry
	for _, e := range entries {
		if e.Type == "message" {
			msgs = append(msgs, e)
		}
	}
	// A turn starts at a user message and runs to just before the next one.
	var starts []int
	for i, e := range msgs {
		if e.Role == "user" {
			starts = append(starts, i)
		}
	}
	total := 0
	pick := len(starts)
	for i := len(starts) - 1; i >= 0; i-- {
		end := len(msgs)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		size := 0
		for _, e := range msgs[starts[i]:end] {
			size += estTokens(e.Text) + estTokens(string(e.ToolResult))
		}
		if total+size > budget {
			break
		}
		total += size
		pick = i
	}
	if pick >= len(starts) {
		return nil
	}
	return msgs[starts[pick]:]
}

// LiveSession follows continued_by to the live session of a chain.
func (a *App) LiveSession(id string) *Session {
	s := a.store.Session(id)
	seen := map[string]bool{}
	for s != nil && s.ContinuedBy != "" && !seen[s.ID] {
		seen[s.ID] = true
		next := a.store.Session(s.ContinuedBy)
		if next == nil {
			break
		}
		s = next
	}
	return s
}

// firstLine keeps a title to one short line whatever the model returns.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	line = strings.Trim(strings.TrimSpace(line), "\"`#*")
	return truncate(line, 60)
}

func (a *App) append(sessionID string, e Entry) Entry {
	out, err := a.store.Append(sessionID, e)
	if err != nil {
		return out
	}
	a.hub.Broadcast(wsEvent{Kind: "entry", SessionID: sessionID, Entry: &out})
	return out
}

func (a *App) appendEvent(sessionID string, e Entry) Entry {
	e.Type = "event"
	return a.append(sessionID, e)
}

// describeConfigChange names the parts of a configuration that differ, for the
// entries either side of a rotation. It is what replaced the mid-session
// availability and model-change entries: the same record, at the only point the
// configuration can now move.
func describeConfigChange(from, to SessionConfig) string {
	var parts []string
	if from.Model != to.Model {
		parts = append(parts, "model "+from.Model+" → "+to.Model)
	}
	if !sameSet(from.EnabledTools, to.EnabledTools) {
		parts = append(parts, "tools "+describeSet(from.EnabledTools)+" → "+describeSet(to.EnabledTools))
	}
	if !sameSet(from.EnabledSkills, to.EnabledSkills) {
		parts = append(parts, "skills "+describeSet(from.EnabledSkills)+" → "+describeSet(to.EnabledSkills))
	}
	return strings.Join(parts, "; ")
}

func describeSet(set []string) string {
	if set == nil {
		return "all"
	}
	if len(set) == 0 {
		return "none"
	}
	return strings.Join(set, ", ")
}

// maybeSummarise updates the rolling summary once a session has grown enough.
// The update reads only the previous summary and the entries since it.
func (a *App) maybeSummarise(ctx context.Context, s *Session) {
	entries := a.store.Entries(s.ID)
	var added []Entry
	grown := 0
	for _, e := range entries {
		if e.Seq >= s.SummarySeq && e.Type == "message" {
			added = append(added, e)
			grown += estTokens(e.Text)
		}
	}
	if grown < a.cfg.SummaryEvery || len(added) == 0 {
		return
	}
	var sb strings.Builder
	if s.Summary != "" {
		fmt.Fprintf(&sb, "Previous summary:\n%s\n\n", s.Summary)
	}
	sb.WriteString("New messages since:\n")
	for _, e := range added {
		fmt.Fprintf(&sb, "%s: %s\n", e.Role, truncate(e.Text, 2000))
	}
	sb.WriteString("\nWrite the updated summary. Keep it under 250 words. State decisions, open questions, and anything a successor conversation would need. No preamble.")

	res, err := a.or.Chat(ctx, ChatRequest{Model: s.Model, Messages: []ChatMessage{
		{Role: "system", Content: "You maintain a rolling summary of a conversation."},
		{Role: "user", Content: sb.String()},
	}}, nil)
	if err != nil {
		return
	}
	now := time.Now()
	s.Summary = strings.TrimSpace(res.Text)
	s.SummaryUpdated = &now
	s.SummarySeq = len(entries)
	s.Cost += res.Usage.Cost
	_ = a.store.PutSession(s)
	a.appendEvent(s.ID, Entry{EventKind: "summary",
		Text: fmt.Sprintf("summary updated (%d messages since last)", len(added)), Usage: &res.Usage})
}

// maybeRotate opens a successor when the session passes its size threshold.
// It runs only between turns.
func (a *App) maybeRotate(s *Session) *Session {
	if s.Status != "active" {
		return s
	}
	if projectedTokens(a.store.Entries(s.ID)) < s.RotateAtTokens {
		return s
	}
	succ, err := a.Rotate(s, s.SessionConfig, true, "size")
	if err != nil {
		return s
	}
	a.Notify(Notification{Title: "Conversation rotated",
		Body: fmt.Sprintf("%q continues in a new session.", s.Title), SessionID: succ.ID, Silent: true})
	return succ
}

// titleIfNeeded gives a session a title within one turn of its first user message.
func (a *App) titleIfNeeded(ctx context.Context, s *Session, firstUserText string) {
	if s.Title != "New session" && s.Title != "" {
		return
	}
	res, err := a.or.Chat(ctx, ChatRequest{Model: s.Model, Messages: []ChatMessage{
		{Role: "user", Content: "Title this conversation in at most six words. Reply with the title alone, no quotes.\n\n" + truncate(firstUserText, 1000)},
	}}, nil)
	s.Title = firstLine(res.Text)
	if err != nil || s.Title == "" {
		s.Title = firstLine(firstUserText)
	}
	s.Cost += res.Usage.Cost
	_ = a.store.PutSession(s)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
}
