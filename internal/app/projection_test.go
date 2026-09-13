package app

import (
	"strings"
	"testing"
	"time"
)

func tick(job, status string, at time.Time) Entry {
	return Entry{Type: "event", EventKind: "job_check", JobID: job, Status: status, CreatedAt: at}
}

func TestProjectionDropsEventsAndCollapsesTrailingTicks(t *testing.T) {
	base := time.Now().Add(-3 * time.Hour)
	entries := []Entry{
		{Type: "prompt", Text: "system prompt"},
		{Type: "message", Role: "user", Text: "ping me when the deploy finishes"},
		{Type: "message", Role: "assistant", Text: "watching"},
		{Type: "event", EventKind: "rotation", Text: "continued from an earlier session"},
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

// The split falls on a turn boundary, so an assistant message is never left
// behind by the user message that prompted it.
func TestSplitPointTakesWholeTurns(t *testing.T) {
	entries := []Entry{
		{Seq: 1, Type: "message", Role: "user", Text: "first"},
		{Seq: 2, Type: "message", Role: "assistant", Text: "answer one"},
		{Seq: 3, Type: "message", Role: "user", Text: "second"},
		{Seq: 4, Type: "message", Role: "assistant", Text: "answer two"},
	}
	got := splitPoint(entries, 6)
	if got != 2 {
		t.Fatalf("splitPoint = %d; want 2, the boundary before the last whole turn", got)
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

// A job wake reached the model as a bare user message, indistinguishable from
// the operator typing it. So the model answered the way it answers the operator
// — in the transcript — and the reminder the operator had asked for never left
// the screen they were not looking at. The wake has to say what it is.
func TestAJobWakeIsMarkedAsOneToTheModel(t *testing.T) {
	msgs := Project([]Entry{
		{Type: "message", Role: "user", Text: "Remind the user to feed the cat.",
			JobID: "J1", Status: "fired"},
	})
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	got, _ := msgs[0].Content.(string)
	if !strings.Contains(got, "J1") {
		t.Errorf("content = %q, want it to name the job", got)
	}
	if !strings.Contains(got, "Remind the user to feed the cat.") {
		t.Errorf("content = %q, want it to carry the job's prompt", got)
	}
	if !strings.Contains(got, "not present") {
		t.Errorf("content = %q, want it to say the operator is not present", got)
	}
}

// A message the operator actually typed must not be dressed up as a job wake:
// the marker is what separates the two, and a false one would make the agent
// interrupt someone who is already reading.
func TestAnOperatorMessageIsPassedThroughUnchanged(t *testing.T) {
	msgs := Project([]Entry{{Type: "message", Role: "user", Text: "hello"}})
	if len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Errorf("got %#v, want the text unchanged", msgs)
	}
}

// A turn that fails leaves its input in the transcript with no answer, because
// entries are append-only and the failure is written as an event — which the
// projection dropped. The next wake appended an identical prompt, so the model
// saw the same instruction twice in a row and answered it twice: the operator
// got "Reminder: time to stretch." twice for one reminder. The failure has to
// reach the model, or an unanswered turn is indistinguishable from a repeat.
func TestAFailedRunIsVisibleToTheModel(t *testing.T) {
	msgs := Project([]Entry{
		{Type: "message", Role: "user", Text: "Remind me to stretch.", JobID: "J1", Status: "fired"},
		{Type: "event", EventKind: "job_error", JobID: "J1", Text: "job run failed: context deadline exceeded"},
		{Type: "message", Role: "user", Text: "Remind me to stretch.", JobID: "J1", Status: "fired"},
	})
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 — the failure between the two wakes is missing", len(msgs))
	}
	mid, _ := msgs[1].Content.(string)
	if !strings.Contains(mid, "no reply") {
		t.Errorf("middle message = %q, want it to say the run produced no reply", mid)
	}
	if !strings.Contains(mid, "context deadline exceeded") {
		t.Errorf("middle message = %q, want it to carry the failure", mid)
	}
}

// Only a failure. Every other event stays out of the request, which is the
// whole reason the transcript can hold a running commentary the model is not
// billed for on every turn.
func TestOrdinaryEventsAreStillDropped(t *testing.T) {
	msgs := Project([]Entry{
		{Type: "event", EventKind: "forked_from", Text: "copied from another session"},
		{Type: "event", EventKind: "dead_letter", Text: "job gave up"},
		{Type: "message", Role: "user", Text: "hello"},
	})
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
}

// A wake that fired long after it was due said its piece as though it were on
// time: a host asleep from four to six delivered "time to stretch" at six with
// nothing to say it was ninety minutes stale. The wake carries when it was due,
// so the agent can tell the operator what it is looking at.
func TestALateWakeSaysHowLateItIs(t *testing.T) {
	due := time.Date(2026, 9, 3, 16, 30, 0, 0, time.UTC)
	msgs := Project([]Entry{{
		Type: "message", Role: "user", Text: "Remind me to stretch.",
		JobID: "J1", Status: "fired", DueAt: due,
		CreatedAt: due.Add(92 * time.Minute),
	}})
	got, _ := msgs[0].Content.(string)
	if !strings.Contains(got, "late") {
		t.Errorf("content = %q, want it to say the wake was late", got)
	}
	if !strings.Contains(got, "1h32m") {
		t.Errorf("content = %q, want it to name how late", got)
	}
	if !strings.Contains(got, "Remind me to stretch.") {
		t.Errorf("content = %q, want it to carry the prompt", got)
	}
}

// A wake is always a little late — the scheduler ticks once a second — and
// saying so every time would be noise that means nothing.
func TestAPunctualWakeSaysNothingAboutTime(t *testing.T) {
	due := time.Date(2026, 9, 3, 16, 30, 0, 0, time.UTC)
	msgs := Project([]Entry{{
		Type: "message", Role: "user", Text: "Remind me to stretch.",
		JobID: "J1", Status: "fired", DueAt: due,
		CreatedAt: due.Add(3 * time.Second),
	}})
	got, _ := msgs[0].Content.(string)
	if strings.Contains(got, "late") {
		t.Errorf("content = %q, want no mention of lateness", got)
	}
}

// A wake stored before this was recorded has no due time, and must project the
// way it always did rather than claiming to be punctual or late.
func TestAWakeWithNoDueTimeIsUnchanged(t *testing.T) {
	msgs := Project([]Entry{{
		Type: "message", Role: "user", Text: "Remind me to stretch.", JobID: "J1",
	}})
	got, _ := msgs[0].Content.(string)
	if strings.Contains(got, "late") {
		t.Errorf("content = %q, want no mention of lateness", got)
	}
}
