package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// nextRun resolves a schedule to its next firing after from. A schedule is an
// interval (2m), a cron expression (0 9 * * *), or an RFC 3339 instant.
func nextRun(schedule string, from time.Time) (time.Time, error) {
	s := strings.TrimSpace(schedule)
	if d, err := time.ParseDuration(s); err == nil {
		if d < time.Second {
			return time.Time{}, fmt.Errorf("interval %s is too short", s)
		}
		return from.Add(d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		if t.After(from) {
			return t, nil
		}
		return time.Time{}, errOneShotDone
	}
	if len(strings.Fields(s)) == 5 {
		return nextCron(s, from)
	}
	return time.Time{}, fmt.Errorf("unrecognised schedule %q", schedule)
}

type oneShotDone struct{}

func (oneShotDone) Error() string { return "one-shot schedule already passed" }

var errOneShotDone = oneShotDone{}

// scheduleInterval reports the gap between firings, or 0 if it is not periodic.
func scheduleInterval(schedule string) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(schedule)); err == nil {
		return d
	}
	if len(strings.Fields(schedule)) == 5 {
		now := time.Now().Truncate(time.Minute)
		a, err := nextCron(schedule, now)
		if err != nil {
			return 0
		}
		b, err := nextCron(schedule, a)
		if err != nil {
			return 0
		}
		return b.Sub(a)
	}
	return 0
}

func nextCron(expr string, from time.Time) (time.Time, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return time.Time{}, fmt.Errorf("cron needs five fields")
	}
	mins, err := cronField(f[0], 0, 59)
	if err != nil {
		return time.Time{}, err
	}
	hours, err := cronField(f[1], 0, 23)
	if err != nil {
		return time.Time{}, err
	}
	doms, err := cronField(f[2], 1, 31)
	if err != nil {
		return time.Time{}, err
	}
	months, err := cronField(f[3], 1, 12)
	if err != nil {
		return time.Time{}, err
	}
	dows, err := cronField(f[4], 0, 6)
	if err != nil {
		return time.Time{}, err
	}
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if months[int(t.Month())] && doms[t.Day()] && dows[int(t.Weekday())] &&
			hours[t.Hour()] && mins[t.Minute()] {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron %q never fires", expr)
}

func cronField(spec string, lo, hi int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(spec, ",") {
		step := 1
		if base, st, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(st)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("bad step in %q", spec)
			}
			step, part = n, base
		}
		start, end := lo, hi
		if part != "*" {
			if a, b, ok := strings.Cut(part, "-"); ok {
				var err error
				if start, err = strconv.Atoi(a); err != nil {
					return nil, fmt.Errorf("bad range in %q", spec)
				}
				if end, err = strconv.Atoi(b); err != nil {
					return nil, fmt.Errorf("bad range in %q", spec)
				}
			} else {
				n, err := strconv.Atoi(part)
				if err != nil {
					return nil, fmt.Errorf("bad value in %q", spec)
				}
				start, end = n, n
			}
		}
		if start < lo || end > hi || start > end {
			return nil, fmt.Errorf("%q out of range %d-%d", spec, lo, hi)
		}
		for v := start; v <= end; v += step {
			out[v] = true
		}
	}
	return out, nil
}
