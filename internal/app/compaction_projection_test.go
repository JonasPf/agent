package app

import (
	"strings"
	"testing"
	"time"
)

func msg(seq int, role, text string) Entry {
	return Entry{Seq: seq, Type: "message", Role: role, Text: text,
		CreatedAt: time.Now()}
}

func compactionAt(seq, covers int, text string) Entry {
	return Entry{Seq: seq, Type: "compaction", CoversThrough: covers, Text: text,
		CreatedAt: time.Now()}
}

func contentOf(m ChatMessage) string {
	s, _ := m.Content.(string)
	return s
}

// The whole of rule 3: what a compaction covers is not sent, and what it says
// is sent in its place.
func TestCompactionReplacesWhatItCovers(t *testing.T) {
	entries := []Entry{
		msg(1, "user", "old question"),
		msg(2, "assistant", "old answer"),
		compactionAt(3, 2, "## Decisions\n- settled the old thing"),
		msg(4, "user", "new question"),
	}
	got := Project(entries)
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(got), got)
	}
	if got[0].Role != "assistant" {
		t.Errorf("compaction projected as %q; want assistant", got[0].Role)
	}
	if !strings.Contains(contentOf(got[0]), "settled the old thing") {
		t.Errorf("compaction text missing: %q", contentOf(got[0]))
	}
	if contentOf(got[1]) != "new question" {
		t.Errorf("tail = %q; want %q", contentOf(got[1]), "new question")
	}
	for _, m := range got {
		if strings.Contains(contentOf(m), "old question") {
			t.Fatal("a covered entry reached the model")
		}
	}
}

// Compactions are cumulative, never stacked: only the newest is sent.
func TestOnlyTheNewestCompactionIsSent(t *testing.T) {
	entries := []Entry{
		msg(1, "user", "first"),
		compactionAt(2, 1, "SUMMARY ONE"),
		msg(3, "user", "second"),
		compactionAt(4, 3, "SUMMARY TWO"),
		msg(5, "user", "third"),
	}
	got := Project(entries)
	joined := ""
	for _, m := range got {
		joined += contentOf(m)
	}
	if strings.Contains(joined, "SUMMARY ONE") {
		t.Error("the superseded compaction was sent")
	}
	if !strings.Contains(joined, "SUMMARY TWO") {
		t.Error("the newest compaction was not sent")
	}
	if strings.Contains(joined, "second") {
		t.Error("an entry the newest compaction covers was sent")
	}
	if !strings.Contains(joined, "third") {
		t.Error("the tail after the newest compaction was dropped")
	}
}

// A transcript with no compaction must project exactly as it always did.
func TestProjectionUnchangedWithoutACompaction(t *testing.T) {
	entries := []Entry{msg(1, "user", "hello"), msg(2, "assistant", "hi")}
	if got := Project(entries); len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
}

func TestProjectionIsStillPure(t *testing.T) {
	entries := []Entry{
		msg(1, "user", "a"),
		compactionAt(2, 1, "summary"),
		msg(3, "user", "b"),
	}
	a, b := Project(entries), Project(entries)
	if len(a) != len(b) {
		t.Fatalf("not pure: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if contentOf(a[i]) != contentOf(b[i]) {
			t.Fatalf("not pure at %d", i)
		}
	}
}

// Rule 1: the newest prompt entry is the system message and the earlier ones
// are dropped. A compaction writes a fresh one, so a session has several.
func TestNewestPromptWins(t *testing.T) {
	entries := []Entry{
		{Seq: 1, Type: "prompt", Sections: []Section{{Name: "memory", Text: "OLD MEMORY"}}},
		msg(2, "user", "a"),
		{Seq: 3, Type: "prompt", Sections: []Section{{Name: "memory", Text: "NEW MEMORY"}}},
		compactionAt(4, 2, "summary"),
		msg(5, "user", "b"),
	}
	got := NewestPrompt(entries)
	if got == nil {
		t.Fatal("no prompt entry found")
	}
	if got.Seq != 3 {
		t.Fatalf("newest prompt is seq %d; want 3", got.Seq)
	}
}

func TestNewestPromptOnASessionThatNeverCompacted(t *testing.T) {
	entries := []Entry{
		{Seq: 1, Type: "prompt", Sections: []Section{{Name: "memory", Text: "M"}}},
		msg(2, "user", "a"),
	}
	if got := NewestPrompt(entries); got == nil || got.Seq != 1 {
		t.Fatalf("got %+v; want seq 1", got)
	}
}
