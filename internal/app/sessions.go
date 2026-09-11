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
// writes its prompt entry at position zero. The configuration is chosen before
// the session exists and is fixed once it does, so there is no window in which a
// session is live and its prompt is still open to change.
func (a *App) NewSession(cfg SessionConfig, forkedFrom string) (*Session, error) {
	if cfg.Model == "" {
		cfg.Model = a.cfg.DefaultModel
	}
	// A session always gets a positive threshold. A zero would mean every turn
	// is over it, so a configuration that forgot to set one would compact on
	// every single turn rather than never.
	compactAt, keep := a.cfg.CompactAtTokens, a.cfg.KeepVerbatimTokens
	if compactAt <= 0 {
		compactAt = defaultCompactAtTokens
	}
	if keep <= 0 {
		keep = defaultKeepVerbatimTokens
	}
	now := time.Now()
	s := &Session{
		ID:                 newID(),
		Title:              "New session",
		SessionConfig:      cfg,
		Status:             "active",
		ForkedFrom:         forkedFrom,
		CompactAtTokens:    compactAt,
		KeepVerbatimTokens: keep,
		CreatedAt:          now,
		LastActiveAt:       now,
	}
	if err := a.store.PutSession(s); err != nil {
		return nil, err
	}
	a.ensureWorkspace(s.ID)
	sections := a.systemSections(s)
	a.append(s.ID, Entry{Type: "prompt", Sections: sections,
		Text: fmt.Sprintf("system prompt · %d tokens", sectionsTotal(sections))})
	return s, nil
}

// Fork copies a conversation into a new session. Everything comes across: the
// whole transcript, compactions and all, and a copy of the working directory.
// The origin is untouched, stays active, and keeps its jobs.
//
// It is the only way to change a configuration, and it is also how a
// conversation is branched. Both intentions are the same act, so there is one
// operation rather than three that differ only in name.
func (a *App) Fork(origin *Session, cfg SessionConfig) (*Session, error) {
	fork, err := a.NewSession(cfg, origin.ID)
	if err != nil {
		return nil, err
	}
	why := "fork"
	if changed := describeConfigChange(origin.SessionConfig, fork.SessionConfig); changed != "" {
		why += ", " + changed
	}
	// A fork carries files as well as words: it starts from a copy of the
	// origin's directory, and neither session's writes reach the other
	// afterwards.
	if err := copyTree(a.sessionWorkspace(origin.ID), a.ensureWorkspace(fork.ID)); err != nil {
		return nil, err
	}

	entries := a.store.Entries(origin.ID)
	a.append(fork.ID, Entry{Type: "event", EventKind: "forked_from", CarriedFrom: origin.ID,
		Text: fmt.Sprintf("copied from %s (%s): %d entries", origin.ID, why, len(entries))})
	for _, e := range entries {
		// The origin's own prompt entry is not copied: the fork has one of its
		// own, written from the configuration this fork was created under, and
		// two would leave the newest — the wrong one — in force.
		if e.Type == "prompt" {
			continue
		}
		e.CarriedFrom = origin.ID
		e.Usage = nil
		a.append(fork.ID, e)
	}

	// Jobs do not move. A job belongs to the conversation it was created in,
	// which still exists and is still running.
	a.append(origin.ID, Entry{Type: "event", EventKind: "fork",
		Text: fmt.Sprintf("copied into %s (%s)", fork.ID, why)})
	fork.Title = origin.Title
	_ = a.store.PutSession(fork)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	return fork, nil
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
// entries either side of a fork. It is what replaced the mid-session
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
