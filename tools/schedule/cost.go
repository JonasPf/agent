package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Job is as much of a job as this tool reads.
type Job struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	Schedule    string   `json:"schedule"`
	Check       string   `json:"check"`
	Prompt      string   `json:"prompt"`
	AfterActing string   `json:"after_acting"`
	NextRunAt   string   `json:"next_run_at"`
	RunCount    int      `json:"run_count"`
	Estimate    Estimate `json:"estimate"`
}

// Estimate is what the system worked out a job will send.
type Estimate struct {
	Present      bool    `json:"-"`
	Gated        bool    `json:"gated"`
	WakesPerDay  float64 `json:"wakes_per_day"`
	TokensPerRun int     `json:"tokens_per_turn"`
	TokensPerDay int     `json:"tokens_per_day"`
	Priced       bool    `json:"priced"`
	CostPerDay   float64 `json:"cost_per_day"`
	CostPerMonth float64 `json:"cost_per_month"`
}

// UnmarshalJSON records whether an estimate was there at all. A job with none
// says nothing about cost, which is different from one that costs nothing.
func (e *Estimate) UnmarshalJSON(b []byte) error {
	type raw Estimate
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*e = Estimate(r)
	e.Present = string(b) != "null" && string(b) != "{}"
	return nil
}

// Cost is what this job will send, and what that comes to. Nothing refuses a
// cadence any more (ADR-029), so the price is what the operator decides on and
// it has to be visible at the moment one is chosen.
func Cost(j Job) string {
	e := j.Estimate
	if !e.Present {
		return ""
	}
	if e.Gated {
		return " · gated by its check: no model cost until it passes, then ~" +
			commas(e.TokensPerRun) + " tok a turn"
	}
	if e.WakesPerDay == 0 {
		return " · one wake, ~" + commas(e.TokensPerRun) + " tok"
	}
	out := fmt.Sprintf(" · ~%.1f wakes a day, ~%s tok a day", e.WakesPerDay, commas(e.TokensPerDay))
	if e.Priced {
		out += fmt.Sprintf(" (~$%.2f a day, ~$%.2f a month)", e.CostPerDay, e.CostPerMonth)
	}
	return out
}

// commas groups a token count for reading. A number the operator has to count
// the digits of is a number they will not read.
func commas(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
