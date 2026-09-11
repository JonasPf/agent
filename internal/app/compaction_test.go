package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func turn(seq int, userText string, size int) []Entry {
	pad := strings.Repeat("x ", size/2)
	return []Entry{
		{Seq: seq, Type: "message", Role: "user", Text: userText, CreatedAt: time.Now()},
		{Seq: seq + 1, Type: "message", Role: "assistant", Text: pad, CreatedAt: time.Now()},
	}
}

// A turn is covered whole or not at all: never half an exchange.
func TestSplitPointFallsOnATurnBoundary(t *testing.T) {
	var entries []Entry
	seq := 1
	for i := 0; i < 6; i++ {
		entries = append(entries, turn(seq, "q", 400)...)
		seq += 2
	}
	split := splitPoint(entries, 400)
	if split == 0 {
		t.Fatal("nothing selected to compact")
	}
	for _, e := range entries {
		if e.Seq == split && e.Role == "user" {
			t.Fatal("split landed on a user message, cutting a turn in half")
		}
	}
}

// The tail left verbatim must fit the budget.
func TestSplitPointRespectsTheBudget(t *testing.T) {
	var entries []Entry
	seq := 1
	for i := 0; i < 10; i++ {
		entries = append(entries, turn(seq, "q", 200)...)
		seq += 2
	}
	budget := 600
	split := splitPoint(entries, budget)
	tail := 0
	for _, e := range entries {
		if e.Seq > split {
			tail += estTokens(e.Text) + estTokens(string(e.ToolResult))
		}
	}
	if tail > budget {
		t.Fatalf("tail is %d tokens, over the %d budget", tail, budget)
	}
}

// A conversation smaller than the budget has nothing to compact.
func TestSplitPointCompactsNothingWhenEverythingFits(t *testing.T) {
	entries := turn(1, "q", 20)
	if got := splitPoint(entries, 100000); got != 0 {
		t.Fatalf("splitPoint = %d; want 0", got)
	}
}

// Rejecting a summary that is not smaller is what stops a summariser which
// restates from growing the conversation instead of shrinking it.
func TestCompactionRejectsASummaryThatIsNotSmaller(t *testing.T) {
	if acceptSummary("this is a long restatement of everything", 3) {
		t.Fatal("a summary larger than what it covers was accepted")
	}
	if !acceptSummary("short", 500) {
		t.Fatal("a genuinely smaller summary was rejected")
	}
}

// The summariser reads the previous summary and the new entries, never the
// entries the previous summary already covered.
func TestSummariserInputIsIncremental(t *testing.T) {
	prev := "## Decisions\n- the old decision"
	head := []Entry{
		{Seq: 9, Type: "message", Role: "user", Text: "ENTRY SINCE"},
	}
	got := buildCompactionInput(prev, head)
	if !strings.Contains(got, "the old decision") {
		t.Error("previous summary missing from the input")
	}
	if !strings.Contains(got, "ENTRY SINCE") {
		t.Error("new entries missing from the input")
	}
	if strings.Contains(got, "OLD RAW ENTRY") {
		t.Error("raw covered entries leaked into the input")
	}
}

func TestSummariserInputWithNoPreviousSummary(t *testing.T) {
	got := buildCompactionInput("", []Entry{
		{Seq: 1, Type: "message", Role: "user", Text: "FIRST"},
	})
	if !strings.Contains(got, "FIRST") {
		t.Error("entries missing")
	}
	if strings.Contains(strings.ToLower(got), "previous") {
		t.Error("announced a previous summary that does not exist")
	}
}

func TestCountFoldedTurns(t *testing.T) {
	entries := append(turn(1, "a", 10), turn(3, "b", 10)...)
	if got := countTurns(entries); got != 2 {
		t.Fatalf("countTurns = %d; want 2", got)
	}
}

// One tool result can be larger than the whole verbatim budget. The tail must
// still hold the turn the conversation is in the middle of, rather than folding
// everything and leaving the model nothing verbatim at all.
func TestSplitPointKeepsTheLastTurnEvenWhenItOverflows(t *testing.T) {
	entries := append(turn(1, "old", 100), turn(3, "huge", 20000)...)
	split := splitPoint(entries, 500)
	if split != 2 {
		t.Fatalf("splitPoint = %d; want 2, keeping the oversized final turn", split)
	}
}

// A single oversized turn and nothing else has nothing to compact.
func TestSplitPointWithOneOversizedTurnCompactsNothing(t *testing.T) {
	if got := splitPoint(turn(1, "huge", 20000), 500); got != 0 {
		t.Fatalf("splitPoint = %d; want 0", got)
	}
}

// The banner's own numbers must describe the fold, not the conversation as it
// stood before it. Measuring the stored entries would report no change at all.
func TestCompactionRecordsAGenuineSaving(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- short")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	s.CompactAtTokens = 3000
	s.KeepVerbatimTokens = 500
	_ = a.store.PutSession(s)
	growWithToolResults(t, a, s, 6)

	if err := a.Compact(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	entries := a.store.Entries(s.ID)
	c := entries[newestCompaction(entries)]
	if c.TokensAfter >= c.TokensBefore {
		t.Fatalf("banner reports %d → %d; a compaction must record a saving",
			c.TokensBefore, c.TokensAfter)
	}
	if got := projectedTokens(entries); got != c.TokensAfter {
		t.Fatalf("banner says %d tokens after; the projection is actually %d", c.TokensAfter, got)
	}
	if c.FoldedTurns == 0 {
		t.Fatal("banner reports folding no turns")
	}
}
