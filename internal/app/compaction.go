package app

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The two thresholds are the whole trade between cost and retention, and they
// are independent. The first is what a call costs at its most expensive; the
// second is how much of the conversation is never lossy. Each compaction frees
// the difference, so the tail is kept well under the threshold — close together
// would compact constantly for very little gain.
const (
	defaultCompactAtTokens    = 40000
	defaultKeepVerbatimTokens = 10000
)

// The summariser is asked to edit rather than to re-summarise. Material that has
// been compacted ten times has passed through ten model calls, and a summary
// rewritten each time degrades like a photocopy of a photocopy. Copying
// unchanged lines through confines the loss to what was actually touched.
const compactionSystem = `You are updating a running record of a conversation so it can continue without the original messages.

You will be given the record so far, then the messages since. Produce the updated record.

RULES
1. Copy existing lines through UNCHANGED unless the new messages supersede them. You are editing a document, not rewriting it.
2. Delete a line only when something later contradicts or completes it.
3. State facts as facts. Never "we discussed the database" — write what was decided about it.
4. Keep exact identifiers: file paths, function names, numbers, flags, error text, URLs.
5. Drop pleasantries, and drop reasoning whose conclusion is already recorded.
6. Under 800 tokens. If you must cut, cut oldest Facts first.

OUTPUT — these four headings, in this order, omitting any that would be empty:

## Decisions      what was settled, and the one-line why
## Open           questions unanswered, work in flight
## Facts          paths, names, numbers, shapes discovered
## Rejected       what was tried and did not work, so it is not retried

Output the record and nothing else. No preamble.`

// splitPoint returns the sequence number through which entries should be
// compacted: the newest turn boundary that leaves a tail fitting the budget. A
// turn is a user message and everything after it up to the next one, and it is
// covered whole or not at all — never half an exchange, and never an assistant
// message separated from the tool results it produced.
func splitPoint(entries []Entry, budget int) int {
	var msgs []Entry
	for _, e := range entries {
		if e.Type == "message" {
			msgs = append(msgs, e)
		}
	}
	var starts []int
	for i, e := range msgs {
		if e.Role == "user" {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return 0
	}
	// Walk turns backwards, accumulating until the next one would not fit. What
	// is left in front of that is what gets compacted.
	total := 0
	keepFrom := len(starts)
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
		keepFrom = i
	}
	if keepFrom == len(starts) {
		// Not even the newest turn fits the budget — one tool result can be
		// larger than the whole tail. Keep it anyway: a turn is covered whole or
		// not at all, and compacting the exchange the conversation is in the
		// middle of would leave the model with no verbatim context at all.
		keepFrom = len(starts) - 1
	}
	if keepFrom == 0 {
		return 0 // everything fits; nothing to compact
	}
	// Cover through the entry immediately before the first kept turn.
	return msgs[starts[keepFrom]].Seq - 1
}

// countTurns counts user messages, which is how many exchanges a fold stands
// for. It is what the banner reports.
func countTurns(entries []Entry) int {
	n := 0
	for _, e := range entries {
		if e.Type == "message" && e.Role == "user" {
			n++
		}
	}
	return n
}

// acceptSummary refuses a summary that is not smaller than what it replaces. A
// summariser that restates rather than summarises must not be able to grow the
// conversation it was called to shrink.
func acceptSummary(text string, coveredTokens int) bool {
	t := strings.TrimSpace(text)
	return t != "" && estTokens(t) < coveredTokens
}

// buildCompactionInput flattens what the summariser reads: the record so far,
// then the entries since it. The previous compaction's text stands in for
// everything it covered, so the cost of a compaction depends on how much was
// added since the last one and not on how long the conversation has become.
func buildCompactionInput(previous string, head []Entry) string {
	var sb strings.Builder
	if strings.TrimSpace(previous) != "" {
		sb.WriteString("=== THE RECORD SO FAR ===\n")
		sb.WriteString(previous)
		sb.WriteString("\n\n")
	}
	sb.WriteString("=== MESSAGES SINCE ===\n")
	for _, e := range head {
		m, ok := toMessage(e)
		if !ok {
			continue
		}
		fmt.Fprintf(&sb, "[%s] %s\n", m.Role, truncate(messageText(m), 2000))
	}
	return sb.String()
}

// Compact folds the oldest turns of a session into a written summary, in place.
// The session keeps its identifier, its jobs, and its files; the transcript
// gains two entries and loses none.
//
// The prompt is refreshed here and only here. A compaction rewrites the head of
// the message list and so discards the cached prefix regardless, which makes it
// the one moment a session's prompt can change for nothing — and the only way
// memory written during a long conversation ever reaches it.
func (a *App) Compact(ctx context.Context, s *Session) error {
	entries := a.store.Entries(s.ID)
	before := projectedTokens(entries)

	var previous string
	covered := 0
	if i := newestCompaction(entries); i >= 0 {
		previous = entries[i].Text
		covered = entries[i].CoversThrough
	}

	split := splitPoint(entries, s.KeepVerbatimTokens)
	if split <= covered {
		return nil // nothing new to fold
	}

	var head []Entry
	headTokens := 0
	for _, e := range entries {
		if e.Seq > covered && e.Seq <= split && e.Type == "message" {
			head = append(head, e)
			headTokens += estTokens(e.Text) + estTokens(string(e.ToolResult))
		}
	}
	if len(head) == 0 {
		return nil
	}

	res, err := a.or.Chat(ctx, ChatRequest{Model: s.Model, Messages: []ChatMessage{
		{Role: "system", Content: compactionSystem},
		{Role: "user", Content: buildCompactionInput(previous, head)},
	}}, nil)
	if err != nil {
		a.appendEvent(s.ID, Entry{EventKind: "error",
			Text: fmt.Sprintf("compaction failed: %v — the conversation continues uncompacted", err)})
		return err
	}
	text := strings.TrimSpace(res.Text)
	if !acceptSummary(text, headTokens) {
		a.appendEvent(s.ID, Entry{EventKind: "error",
			Text: "compaction produced no saving and was discarded — the conversation continues uncompacted"})
		return fmt.Errorf("summary not smaller than what it covers")
	}

	s.Cost += res.Usage.Cost

	// The prompt first, so it is in force for the turn that follows. Its memory
	// section is read now, which is what makes a memory written during this
	// conversation finally reach it.
	items, _ := a.store.Memory()
	sections := a.systemSections(s)
	a.append(s.ID, Entry{Type: "prompt", Sections: sections,
		Text: fmt.Sprintf("system prompt · %d tokens · memory refreshed, %d items",
			sectionsTotal(sections), len(items))})

	// The figures are stored rather than recomputed later, so the banner cannot
	// drift if the estimator changes. TokensAfter is measured on the list this
	// compaction is about to produce — projecting the stored entries here would
	// measure the conversation as it still is, before the fold takes effect.
	folded := Entry{
		Type:          "compaction",
		CoversThrough: split,
		Text:          text,
		FoldedTurns:   countTurns(head),
		TokensBefore:  before,
	}
	folded.TokensAfter = projectedTokens(append(a.store.Entries(s.ID), folded))
	a.append(s.ID, folded)

	s.LastCompactedAt = ptrTime(time.Now())
	_ = a.store.PutSession(s)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	return nil
}

func ptrTime(t time.Time) *time.Time { return &t }

// maybeCompact folds a session that has passed its size threshold. It runs only
// between turns. A failure leaves the conversation running, over threshold and
// visibly so, and is retried after the next turn.
func (a *App) maybeCompact(ctx context.Context, s *Session) {
	if s.Status != "active" || s.CompactAtTokens <= 0 {
		return
	}
	if projectedTokens(a.store.Entries(s.ID)) < s.CompactAtTokens {
		return
	}
	_ = a.Compact(ctx, s)
}
