package app

import (
	"fmt"
	"strings"
	"time"
)

// A comparison is one question put to several models at once. It is built out
// of forks rather than out of a second way of running a turn: a candidate needs
// the whole conversation, a working directory nobody else writes to, and the
// full turn — tools included — that any other session gets. Forking already
// provides all three, and separate sessions already run concurrently
// (ADR-056).
//
// The origin is not sent the message. It keeps its identifier, its jobs, and
// its place in the list, and records one event saying what was asked and who
// was asked it. Whichever candidate is kept is the conversation from then on.

// howLongToStopACandidate bounds the wait for a discarded candidate's turn to
// notice it was cancelled. It is a courtesy rather than a guarantee: the point
// is not to delete a session out from under a turn still writing entries, and
// a model that ignores a closed connection must not hold up the choice.
const howLongToStopACandidate = 2 * time.Second

// Compare forks the origin once per model and puts the same message to each
// fork. The candidates come back in the order the models were given.
func (a *App) Compare(origin *Session, text string, models []string) (string, []*Session, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil, fmt.Errorf("a comparison needs a message to put to the models")
	}
	if len(models) < 2 {
		return "", nil, fmt.Errorf("a comparison needs at least two models; %d were chosen", len(models))
	}
	group := newID()
	var cands []*Session
	var named []string
	for _, m := range models {
		cfg := origin.SessionConfig
		cfg.Model = m
		c, err := a.Fork(origin, cfg)
		if err != nil {
			return "", nil, err
		}
		c.Comparison = group
		// Three rows under one title cannot be told apart in a list, so a
		// candidate is titled by the model that makes it different. Keeping one
		// puts the conversation's own title back.
		c.Title = origin.Title + " · " + shortModel(m)
		if err := a.store.PutSession(c); err != nil {
			return "", nil, err
		}
		cands = append(cands, c)
		named = append(named, m+" in "+c.ID)
	}
	a.appendEvent(origin.ID, Entry{EventKind: "compare", Text: fmt.Sprintf(
		"comparison %s: %q put to %d models — %s",
		group, truncate(strings.TrimSpace(text), 200), len(models), strings.Join(named, ", "))})
	// Sent only once every candidate exists, so the event that names them is
	// already in the origin when the first answer starts arriving.
	for _, c := range cands {
		if err := a.SendUserMessage(c.ID, text); err != nil {
			return "", nil, err
		}
	}
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	return group, cands, nil
}

// Keep ends a comparison by choosing one of its candidates. The chosen session
// carries on as an ordinary conversation and every other candidate is deleted,
// transcript and working directory with it. It reports what it deleted.
func (a *App) Keep(winner *Session) ([]string, error) {
	if winner.Comparison == "" {
		return nil, fmt.Errorf("session %s is not a candidate of an undecided comparison; "+
			"keeping is how a comparison is decided", winner.ID)
	}
	var discarded []*Session
	for _, other := range a.store.Sessions() {
		if other.ID != winner.ID && other.Comparison == winner.Comparison {
			discarded = append(discarded, other)
		}
	}

	var gone []string
	for _, d := range discarded {
		// A candidate is judged on what it has written so far, which may be
		// before it has finished writing. Stopped first, so nothing is deleted
		// from under a turn still appending to it.
		a.StopTurn(d.ID)
	}
	for _, d := range discarded {
		a.settleBefore(d.ID, howLongToStopACandidate)
		if err := a.DeleteSession(d.ID); err != nil {
			return nil, err
		}
		gone = append(gone, d.Model+" in "+d.ID)
	}

	winner.Comparison = ""
	winner.Title = untitledCandidate(winner.Title)
	if origin := a.store.Session(winner.ForkedFrom); origin != nil {
		winner.Title = origin.Title
		a.appendEvent(origin.ID, Entry{EventKind: "kept", Text: fmt.Sprintf(
			"kept %s in %s; deleted %s", winner.Model, winner.ID, describeDiscarded(gone))})
	}
	if err := a.store.PutSession(winner); err != nil {
		return nil, err
	}
	a.appendEvent(winner.ID, Entry{EventKind: "kept", Text: fmt.Sprintf(
		"kept from a comparison of %d: this conversation is the one on %s; deleted %s",
		len(discarded)+1, winner.Model, describeDiscarded(gone))})
	// The winner is what the next conversation should start on: choosing it here
	// is choosing it, exactly as forking onto a model is.
	a.rememberModel(winner.Model)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	return gone, nil
}

// settleBefore waits, up to a bound, for a session's queue to drain.
func (a *App) settleBefore(sessionID string, d time.Duration) {
	deadline := time.Now().Add(d)
	for a.Busy(sessionID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

func describeDiscarded(gone []string) string {
	if len(gone) == 0 {
		return "nothing else"
	}
	return strings.Join(gone, ", ")
}

// shortModel is the part of an identifier that differs between the models being
// compared. The provider is the same for most of a comparison and the whole id
// does not fit in a title.
func shortModel(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

// untitledCandidate removes the model a candidate was titled with, for the case
// where the origin is gone and cannot say what the title was.
func untitledCandidate(title string) string {
	if i := strings.LastIndex(title, " · "); i > 0 {
		return title[:i]
	}
	return title
}
