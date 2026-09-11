package app

import (
	"fmt"
	"strings"
	"time"
)

// A job's price, shown rather than enforced. The fifteen-minute floor used to
// refuse a cadence the operator asked for, and the agent routed around it at
// greater cost (ADR-029). What replaces it is the number: what this job will
// send, how often, and what that comes to.
type JobEstimate struct {
	// WakesPerDay is zero for a schedule that fires once.
	WakesPerDay float64 `json:"wakes_per_day"`
	// TokensPerTurn is what one wake that acts re-sends: this conversation as it
	// stands. It is the prompt side only; what the model says back is not known
	// in advance.
	TokensPerTurn int `json:"tokens_per_turn"`
	// TokensPerDay is zero for a gated job, whose wakes cost nothing until the
	// check passes, and for a job that wakes once.
	TokensPerDay int     `json:"tokens_per_day"`
	CostPerDay   float64 `json:"cost_per_day,omitempty"`
	CostPerMonth float64 `json:"cost_per_month,omitempty"`
	// Priced is false when the model's price is not known — the catalogue was
	// unreachable, or the model is free. The token figures still hold.
	Priced bool   `json:"priced"`
	Gated  bool   `json:"gated"`
	Note   string `json:"note"`
}

const daysPerMonth = 30

// estimateJob prices a job from its schedule, whether a check gates it, the
// size of the conversation it runs in, and the model's prompt price per token.
// It is a floor rather than a promise, and says so.
func estimateJob(schedule string, gated bool, contextTokens int, promptPrice float64) JobEstimate {
	e := JobEstimate{TokensPerTurn: contextTokens, Gated: gated}
	interval := scheduleInterval(schedule)
	if interval > 0 {
		e.WakesPerDay = float64(24*time.Hour) / float64(interval)
	}

	var notes []string
	switch {
	case gated:
		notes = append(notes, "a check gates this job, so its wakes run a shell command and cost nothing; only a wake that passes the check costs a turn")
	case e.WakesPerDay == 0:
		notes = append(notes, "this job wakes once")
	default:
		e.TokensPerDay = int(e.WakesPerDay * float64(contextTokens))
	}

	if promptPrice > 0 {
		e.Priced = true
		e.CostPerDay = float64(e.TokensPerDay) * promptPrice
		e.CostPerMonth = e.CostPerDay * daysPerMonth
	}
	if e.TokensPerDay > 0 {
		notes = append(notes, "the estimate is this conversation at its current size, and it grows with every turn until the session compacts")
	}
	notes = append(notes, "the prompt side only; what the model says back is not counted")
	e.Note = strings.Join(notes, "; ")
	return e
}

// scheduleInterval reports the gap between firings, or 0 if the schedule fires
// once. It exists to price a job, not to refuse one.
func scheduleInterval(schedule string) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(schedule)); err == nil {
		return d
	}
	if len(strings.Fields(schedule)) == 5 {
		// A cron expression's cadence is the gap between two of its firings. A
		// day of them is measured rather than assumed, because "*/20 9-16 * * 1-5"
		// has no single gap.
		return cronDailyInterval(schedule)
	}
	return 0
}

// cronDailyInterval is the average gap between a cron expression's firings over
// a week, which is what makes a working-hours schedule priceable at all.
func cronDailyInterval(expr string) time.Duration {
	return cronDailyIntervalFrom(expr, time.Now())
}

// cronDailyIntervalFrom measures that week from a given moment. The window is
// half-open at the near end — nextCron reports the firings after `from`, so the
// far end has to be inclusive, or a `from` that lands on a firing minute loses
// it at both ends and prices a week as if it held one firing fewer.
func cronDailyIntervalFrom(expr string, now time.Time) time.Duration {
	const window = 7 * 24 * time.Hour
	from := now.Truncate(time.Minute)
	end := from.Add(window)
	n := 0
	at := from
	for {
		next, err := nextCron(expr, at)
		if err != nil || next.After(end) {
			break
		}
		n++
		at = next
		if n > 20000 {
			break
		}
	}
	if n == 0 {
		return 0
	}
	return window / time.Duration(n)
}

// fmtMoney is how a price is written wherever one is shown.
func fmtMoney(v float64) string {
	if v >= 1 {
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("$%.4f", v)
}
