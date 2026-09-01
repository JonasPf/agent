package app

import "testing"

func TestJobKind(t *testing.T) {
	cases := []struct {
		name     string
		schedule string
		check    string
		want     string
	}{
		{"command decides it", "2m", "test -f /tmp/done", "check"},
		{"reminder at an instant", "2026-09-01T15:05:00+01:00", "", "due"},
		{"instant with a check is still a check", "2026-09-01T15:05:00+01:00", "true", "check"},
		{"repeating with no check", "30m", "", "judgement"},
		{"cron with no check", "0 9 * * *", "", "judgement"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jobKind(&Job{Schedule: c.schedule, Check: c.check}); got != c.want {
				t.Errorf("jobKind(%q, check %q) = %q, want %q", c.schedule, c.check, got, c.want)
			}
		})
	}
}

func TestOneShot(t *testing.T) {
	for _, s := range []string{"2026-09-01T15:05:00+01:00", " 2026-09-01T15:05:00Z "} {
		if !oneShot(s) {
			t.Errorf("oneShot(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"5m", "0 9 * * *", "", "tomorrow"} {
		if oneShot(s) {
			t.Errorf("oneShot(%q) = true, want false", s)
		}
	}
}
