package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The fifteen-minute floor used to refuse a cadence on the system's behalf, and
// the agent routed around it (ADR-029). The price shown here is what replaced
// it, so it has to be right and it has to be readable.
func jobWith(t *testing.T, estimate string) Job {
	t.Helper()
	var j Job
	if err := json.Unmarshal([]byte(`{"id":"J1","estimate":`+estimate+`}`), &j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestCost(t *testing.T) {
	t.Run("a repeating job states a day and a month", func(t *testing.T) {
		line := Cost(jobWith(t, `{"wakes_per_day":144,"tokens_per_turn":5000,
			"tokens_per_day":720000,"priced":true,"cost_per_day":2.16,"cost_per_month":64.8}`))
		for _, want := range []string{"144", "720,000", "$2.16", "$64.80"} {
			if !strings.Contains(line, want) {
				t.Errorf("line %q is missing %q", line, want)
			}
		}
	})
	t.Run("a gated job says its wakes are free", func(t *testing.T) {
		line := Cost(jobWith(t, `{"gated":true,"tokens_per_turn":5000}`))
		if !strings.Contains(line, "no model cost") || !strings.Contains(line, "5,000") {
			t.Errorf("line = %q", line)
		}
	})
	t.Run("a one-shot job is priced once", func(t *testing.T) {
		line := Cost(jobWith(t, `{"tokens_per_turn":3200}`))
		if !strings.Contains(line, "one wake") || !strings.Contains(line, "3,200") {
			t.Errorf("line = %q", line)
		}
	})
	t.Run("an unpriced job still says what it sends", func(t *testing.T) {
		line := Cost(jobWith(t, `{"wakes_per_day":24,"tokens_per_day":120000}`))
		if !strings.Contains(line, "120,000") {
			t.Errorf("line = %q", line)
		}
		if strings.Contains(line, "$") {
			t.Errorf("line %q names a price the system did not have", line)
		}
	})
	t.Run("no estimate says nothing rather than nothing costs anything", func(t *testing.T) {
		if line := Cost(jobWith(t, `{}`)); line != "" {
			t.Errorf("line = %q, want empty", line)
		}
		if line := Cost(jobWith(t, `null`)); line != "" {
			t.Errorf("line = %q, want empty", line)
		}
	})
}

func TestCommas(t *testing.T) {
	for _, c := range []struct {
		in   int
		want string
	}{{0, "0"}, {999, "999"}, {1000, "1,000"}, {720000, "720,000"},
		{1234567, "1,234,567"}, {-5000, "-5,000"}} {
		if got := commas(c.in); got != c.want {
			t.Errorf("commas(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A job that has finished has no next wake, and printing the stale one reads as
// though it will fire again.
func TestLine(t *testing.T) {
	j := jobWith(t, `{}`)
	j.Schedule, j.AfterActing, j.NextRunAt = "every 1h", "reschedule", "2026-09-05T09:00:00Z"
	j.Status, j.RunCount = "done", 3
	line := Line(j)
	if !strings.Contains(line, "next wake (none)") {
		t.Errorf("a finished job still advertises a wake: %q", line)
	}
	if !strings.Contains(line, "check (none)") {
		t.Errorf("a job with no check should say so: %q", line)
	}
}
