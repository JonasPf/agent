package app

import (
	"testing"
	"time"
)

func tick(job, status string, at time.Time) Entry {
	return Entry{Type: "event", EventKind: "job_check", JobID: job, Status: status, CreatedAt: at}
}

func TestProjectionDropsEventsAndCollapsesTrailingTicks(t *testing.T) {
	base := time.Now().Add(-3 * time.Hour)
	entries := []Entry{
		{Type: "event", EventKind: "prompt", Text: "system prompt"},
		{Type: "message", Role: "user", Text: "ping me when the deploy finishes"},
		{Type: "message", Role: "assistant", Text: "watching"},
		{Type: "event", EventKind: "job_created", Text: "job created"},
	}
	for i := 0; i < 5; i++ {
		entries = append(entries, tick("J1", "not_fired", base.Add(time.Duration(i)*time.Minute)))
	}

	got := Project(entries)
	if len(got) != 3 {
		t.Fatalf("want 3 messages, got %d: %+v", len(got), got)
	}
	line, _ := got[2].Content.(string)
	if want := "checked 5 times over 4m0s"; !containsSub(line, want) {
		t.Fatalf("collapsed line %q does not state %q", line, want)
	}
}

func TestOnlyTrailingRunCollapses(t *testing.T) {
	base := time.Now()
	entries := []Entry{
		tick("J1", "not_fired", base),
		tick("J1", "not_fired", base.Add(time.Minute)),
		{Type: "message", Role: "user", Text: "any news?"},
		tick("J1", "not_fired", base.Add(2*time.Minute)),
	}
	got := Project(entries)
	if len(got) != 2 {
		t.Fatalf("want 2 messages, got %d: %+v", len(got), got)
	}
	if got[0].Content != "any news?" {
		t.Fatalf("earlier ticks were not dropped: %+v", got[0])
	}
}

func TestProjectionIsPure(t *testing.T) {
	entries := []Entry{{Type: "message", Role: "user", Text: "hi"}}
	a, b := Project(entries), Project(entries)
	if len(a) != len(b) || a[0].Content != b[0].Content {
		t.Fatal("projection is not pure")
	}
	if len(entries) != 1 {
		t.Fatal("projection mutated its input")
	}
}

func TestCarryOverTakesWholeTurns(t *testing.T) {
	entries := []Entry{
		{Type: "message", Role: "user", Text: "first"},
		{Type: "message", Role: "assistant", Text: "answer one"},
		{Type: "message", Role: "user", Text: "second"},
		{Type: "message", Role: "assistant", Text: "answer two"},
	}
	got := carryOver(entries, 6)
	if len(got) != 2 || got[0].Text != "second" {
		t.Fatalf("want the last whole turn, got %+v", got)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
