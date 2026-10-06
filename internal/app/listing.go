package app

import (
	"sync"
	"time"
)

// The session list is read by every page whenever a count it shows moves, and
// two of its figures are costly to measure: the context a conversation would
// send, which projects and encodes its whole transcript, and what its working
// directory holds, which visits every file in it — a cloned repository is tens
// of thousands. Measured for every session on every read, they made the list
// the most expensive thing the agent serves. So both are kept between reads,
// each for exactly as long as what it measures cannot have changed.

// workspaceSizeTTL bounds how stale a kept directory size can be. Every change
// the agent makes or sees — a tool call, a check, an upload, a delete — forgets
// the kept figure at once; what this catches is the one it cannot see, a
// process a command left running in the background that goes on writing.
const workspaceSizeTTL = time.Minute

type contextMemo struct{ entries, tokens int }

type sizeMemo struct {
	bytes int64
	at    time.Time
}

type listMemos struct {
	mu      sync.Mutex
	context map[string]contextMemo
	size    map[string]sizeMemo
	// changes counts what has been written into each working directory, so a
	// measurement that was walking the tree while something was written is
	// not kept: it may have missed the write.
	changes map[string]int
}

// contextUsed is projectedTokens for a whole transcript, kept per session. A
// transcript only ever grows at the end and the projection reads nothing else,
// so the figure for a given number of entries never changes.
func (a *App) contextUsed(sessionID string, entries []Entry) int {
	m := &a.memos
	m.mu.Lock()
	kept, ok := m.context[sessionID]
	m.mu.Unlock()
	if ok && kept.entries == len(entries) {
		return kept.tokens
	}
	tokens := projectedTokens(entries)
	m.mu.Lock()
	if m.context == nil {
		m.context = map[string]contextMemo{}
	}
	m.context[sessionID] = contextMemo{entries: len(entries), tokens: tokens}
	m.mu.Unlock()
	return tokens
}

// workspaceBytes is what a session's working directory holds. Measured fresh,
// the figure is kept for the list; otherwise the list reads the kept one while
// it is current.
func (a *App) workspaceBytes(sessionID string, fresh bool) int64 {
	m := &a.memos
	m.mu.Lock()
	kept, ok := m.size[sessionID]
	gen := m.changes[sessionID]
	m.mu.Unlock()
	if !fresh && ok && time.Since(kept.at) < workspaceSizeTTL {
		return kept.bytes
	}
	n := dirBytes(a.sessionWorkspace(sessionID))
	m.mu.Lock()
	if m.changes[sessionID] == gen {
		if m.size == nil {
			m.size = map[string]sizeMemo{}
		}
		m.size[sessionID] = sizeMemo{bytes: n, at: time.Now()}
	}
	m.mu.Unlock()
	return n
}

// workspaceChanged forgets a session's kept directory size: something was
// written into it, or taken out.
func (a *App) workspaceChanged(sessionID string) {
	m := &a.memos
	m.mu.Lock()
	delete(m.size, sessionID)
	if m.changes == nil {
		m.changes = map[string]int{}
	}
	m.changes[sessionID]++
	m.mu.Unlock()
}

// forgetSession drops everything kept for a session that no longer exists.
func (a *App) forgetSession(sessionID string) {
	m := &a.memos
	m.mu.Lock()
	delete(m.context, sessionID)
	delete(m.size, sessionID)
	delete(m.changes, sessionID)
	m.mu.Unlock()
}
