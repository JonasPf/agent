package app

import (
	"errors"
	"testing"
	"time"
)

// A schedule answers one question: when is the next wake? A single instant has
// exactly one, which is the whole of how a reminder ends — no rule deletes it,
// the schedule simply runs out.
func TestNextRun(t *testing.T) {
	from := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		schedule string
		want     time.Time
		err      bool
		done     bool
	}{
		{"interval", "30m", from.Add(30 * time.Minute), false, false},
		{"cron", "0 9 * * *", time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC), false, false},
		{"instant ahead", "2026-09-01T18:00:00Z", time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC), false, false},
		{"instant passed", "2026-09-01T06:00:00Z", time.Time{}, true, true},
		{"unrecognised", "tomorrow at nine", time.Time{}, true, false},
		{"interval too short", "500ms", time.Time{}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := nextRun(c.schedule, from)
			if c.err != (err != nil) {
				t.Fatalf("nextRun(%q) err = %v, want err %v", c.schedule, err, c.err)
			}
			if c.done != errors.Is(err, error(errOneShotDone)) {
				t.Fatalf("nextRun(%q) one-shot-done = %v, want %v", c.schedule, err, c.done)
			}
			if !c.err && !got.Equal(c.want) {
				t.Fatalf("nextRun(%q) = %v, want %v", c.schedule, got, c.want)
			}
		})
	}
}
