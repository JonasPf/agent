package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The cost floor is gone, so the operator decides for themselves whether a
// cadence is worth it. That only works if the price is in front of them at the
// moment they choose it, rather than discovered on the bill.
func TestARepeatingJobIsPricedPerDay(t *testing.T) {
	// 10 minutes apart is 144 wakes a day, each re-sending the conversation.
	e := estimateJob("10m", false, 5000, 0.000003)
	if e.WakesPerDay != 144 {
		t.Errorf("wakes per day = %v, want 144", e.WakesPerDay)
	}
	if e.TokensPerTurn != 5000 {
		t.Errorf("tokens per turn = %d, want 5000", e.TokensPerTurn)
	}
	if e.TokensPerDay != 720000 {
		t.Errorf("tokens per day = %d, want 720000", e.TokensPerDay)
	}
	if !e.Priced {
		t.Fatal("a model with a known price must produce a price")
	}
	if got := e.CostPerDay; got < 2.15 || got > 2.17 {
		t.Errorf("cost per day = %v, want about 2.16", got)
	}
	if got := e.CostPerMonth; got < 64 || got > 66 {
		t.Errorf("cost per month = %v, want about 65", got)
	}
}

// A check runs a shell command, so a gated job costs nothing until the thing it
// waits for happens. That is the whole argument for reaching for a check, and
// it is worth showing rather than asserting.
func TestAGatedJobCostsNothingUntilItFires(t *testing.T) {
	e := estimateJob("2m", true, 5000, 0.000003)
	if !e.Gated {
		t.Error("a job with a check must be marked gated")
	}
	if e.TokensPerDay != 0 || e.CostPerDay != 0 {
		t.Errorf("a gated job must estimate nothing per day, got %d tokens / %v", e.TokensPerDay, e.CostPerDay)
	}
	// The cost of the wake that does fire is still worth knowing.
	if e.TokensPerTurn != 5000 {
		t.Errorf("tokens per turn = %d, want the turn a firing wake costs", e.TokensPerTurn)
	}
	if !strings.Contains(e.Note, "check") {
		t.Errorf("note = %q, want it to explain what a check does to the cost", e.Note)
	}
}

// A single instant has no cadence, so a daily figure would be a fiction.
func TestAOneShotIsPricedOnce(t *testing.T) {
	e := estimateJob(time.Now().Add(time.Hour).Format(time.RFC3339), false, 5000, 0.000003)
	if e.WakesPerDay != 0 {
		t.Errorf("wakes per day = %v, want 0 for a single wake", e.WakesPerDay)
	}
	if e.TokensPerDay != 0 {
		t.Errorf("tokens per day = %d, want 0", e.TokensPerDay)
	}
	if e.TokensPerTurn != 5000 {
		t.Errorf("tokens per turn = %d, want the one turn it costs", e.TokensPerTurn)
	}
	if !strings.Contains(e.Note, "once") {
		t.Errorf("note = %q, want it to say the job wakes once", e.Note)
	}
}

// A cron schedule has a cadence too, and a working-hours one has no single gap
// between firings — so it is measured over a week and averaged. Every twenty
// minutes from nine to five, Monday to Friday, is 24 firings on each of 5 days:
// 120 a week, which is what a day of it costs on average.
func TestACronScheduleIsPriced(t *testing.T) {
	if e := estimateJob("0 8 * * *", false, 5000, 0.000003); e.WakesPerDay != 1 {
		t.Errorf("wakes per day = %v, want 1 for a daily cron", e.WakesPerDay)
	}
	e := estimateJob("*/20 9-16 * * 1-5", false, 1000, 0)
	if e.WakesPerDay < 17.1 || e.WakesPerDay > 17.2 {
		t.Errorf("wakes per day = %v, want about 17.14 (120 a week)", e.WakesPerDay)
	}
}

// Without a price the tokens are still worth showing: they are the part that
// does not depend on reaching OpenRouter.
func TestAnUnpricedModelStillReportsTokens(t *testing.T) {
	e := estimateJob("10m", false, 5000, 0)
	if e.Priced {
		t.Error("a model with no known price must not claim one")
	}
	if e.TokensPerDay != 720000 {
		t.Errorf("tokens per day = %d, want the tokens regardless", e.TokensPerDay)
	}
	if e.CostPerDay != 0 {
		t.Errorf("cost per day = %v, want none", e.CostPerDay)
	}
}

// The estimate is a floor, not a promise: a repeating job grows the transcript
// it re-sends, so the figure is what today's conversation costs.
func TestTheNoteSaysWhatTheEstimateIsBasedOn(t *testing.T) {
	e := estimateJob("10m", false, 5000, 0.000003)
	if !strings.Contains(e.Note, "grows") {
		t.Errorf("note = %q, want it to say the conversation grows", e.Note)
	}
}

// The estimate has to reach whoever is choosing the cadence, which means the
// API the tool and the interface both use — not a figure computed somewhere
// only Go can see.
func TestCreatingAJobAnswersWithItsEstimate(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	body := `{"session_id":"` + s.ID + `","schedule":"10m","prompt":"Remind me to stretch."}`
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/jobs", strings.NewReader(body)))
	if w.Code != 201 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var got Job
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Estimate == nil {
		t.Fatal("a created job must answer with what it will cost")
	}
	if got.Estimate.WakesPerDay != 144 {
		t.Errorf("wakes per day = %v, want 144", got.Estimate.WakesPerDay)
	}
	if got.Estimate.TokensPerTurn <= 0 {
		t.Error("the estimate must know how much this conversation sends")
	}
	if got.Estimate.Note == "" {
		t.Error("an estimate must say what it is based on")
	}
}

// The estimate is computed on the way out and never written down: a stored
// price would be wrong by the next turn, since the conversation it prices grows.
func TestTheEstimateIsNotStored(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "10m", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := a.store.Job(j.ID)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if stored.Estimate != nil {
		t.Error("the estimate must not be persisted with the job")
	}
}

// The week a cron cadence is measured over starts at whatever minute the
// operator asks, and a firing may land on that minute exactly. Counting the
// firings strictly inside the window would drop that one and quietly under-price
// the job, so the count must not depend on the phase of the week.
func TestACronCadenceDoesNotDependOnWhenItIsAsked(t *testing.T) {
	// Monday 09:00 is a firing minute of this schedule; 09:07 is not.
	on := time.Date(2026, 9, 7, 9, 0, 0, 0, time.Local)
	off := time.Date(2026, 9, 7, 9, 7, 0, 0, time.Local)
	if a, b := cronDailyIntervalFrom("*/20 9-16 * * 1-5", on), cronDailyIntervalFrom("*/20 9-16 * * 1-5", off); a != b {
		t.Errorf("interval from a firing minute = %v, from a quiet minute = %v; want the same cadence", a, b)
	}
}
