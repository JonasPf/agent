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
	// Seven is Sunday as well as zero, as it is in every crontab.
	dows, err := cronField(f[4], 0, 7)
	if err != nil {
		return time.Time{}, err
	}
	if dows[7] {
		dows[0] = true
	}
	// The two day fields combine as they do in cron: when both are restricted
	// a day either one names fires, so "0 9 1 * 1" is the first of the month
	// and every Monday; when either is a star, the other alone decides. A field
	// is a star when it starts with one, a step over a star included.
	either := !strings.HasPrefix(f[2], "*") && !strings.HasPrefix(f[4], "*")
	day := func(t time.Time) bool {
		if either {
			return doms[t.Day()] || dows[int(t.Weekday())]
		}
		return doms[t.Day()] && dows[int(t.Weekday())]
	}
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if months[int(t.Month())] && day(t) && hours[t.Hour()] && mins[t.Minute()] {
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
		base, st, hasStep := strings.Cut(part, "/")
		if hasStep {
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
				// A step from a single value runs to the end of the range:
				// 1/6 in the hours is 1, 7, 13, and 19.
				if hasStep {
					end = hi
				}
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
