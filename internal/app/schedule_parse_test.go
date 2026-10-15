package app

import (
	"encoding/json"
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
		// Standard cron: with both day fields restricted, either one fires.
		// 1 September 2026 is a Tuesday, so the first of the month has passed
		// at nine and the next Monday is the 7th.
		{"day of month or day of week", "0 9 1 * 1", time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC), false, false},
		{"weekdays or the fifteenth", "0 9 15 * 1-5", time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC), false, false},
		// With either day field a star, the other alone decides.
		{"a star day of month leaves the weekday", "0 9 * * 1", time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC), false, false},
		{"a stepped star day of month is still a star", "0 9 */2 * 1", time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC), false, false},
		{"seven is Sunday", "0 9 * * 7", time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC), false, false},
		{"a step from a start", "0 1/6 * * *", time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC), false, false},
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

// A job's next wake is what its schedule means to anyone who has written a
// crontab. "0 9 1 * 1" used to wait for a Monday that fell on the first,
// which can be months away, where cron fires on the first and on Mondays.
func TestAJobWakesWhenCronWould(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	for _, c := range []struct {
		schedule string
		fires    func(time.Time) bool
		within   time.Duration
	}{
		{"0 9 1 * 1", func(t time.Time) bool { return t.Day() == 1 || t.Weekday() == time.Monday }, 7 * 24 * time.Hour},
		{"0 9 * * 7", func(t time.Time) bool { return t.Weekday() == time.Sunday }, 7 * 24 * time.Hour},
	} {
		w := callAPI(t, a, "POST", "/jobs", map[string]any{"session_id": s.ID, "schedule": c.schedule, "prompt": "Water the beds."})
		if w.Code != 201 {
			t.Errorf("POST /jobs %q: status %d (%s)", c.schedule, w.Code, w.Body.String())
			continue
		}
		var j Job
		if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
			t.Fatal(err)
		}
		if !c.fires(j.NextRunAt) || j.NextRunAt.Hour() != 9 || time.Until(j.NextRunAt) > c.within {
			t.Errorf("%q next wakes at %v, which is not when cron would fire it", c.schedule, j.NextRunAt)
		}
	}
}
