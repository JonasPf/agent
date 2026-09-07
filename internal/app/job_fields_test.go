package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// A job is its fields. Nothing derives a category, so nothing needs an exemption
// from a rule written for another category.
func TestAJobCarriesNoKind(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "30m", Prompt: "look"})
	if err != nil {
		t.Fatal(err)
	}
	b := jobJSON(t, a, j.ID)
	if _, ok := b["kind"]; ok {
		t.Error("a job must not expose a kind")
	}
	for _, gone := range []string{"on_condition_met", "reason_no_check", "expires_at", "repeat", "until"} {
		if _, ok := b[gone]; ok {
			t.Errorf("field %q must be gone", gone)
		}
	}
	for _, want := range []string{"schedule", "check", "prompt", "after_acting", "status"} {
		if _, ok := b[want]; !ok {
			t.Errorf("field %q must be present", want)
		}
	}
}

// The bug the old design had: a reminder further out than any default deadline
// was deleted before its time arrived. Nothing expires now, so a job scheduled
// six months out simply waits.
func TestAJobFarInTheFutureSimplyWaits(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	birthday := time.Now().AddDate(0, 6, 0).Format(time.RFC3339)
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: birthday,
		Prompt: "Wish them happy birthday."})
	if err != nil {
		t.Fatal(err)
	}
	if j.NextRunAt.Before(time.Now().AddDate(0, 5, 0)) {
		t.Errorf("next wake = %v, want six months out", j.NextRunAt)
	}
	if j.Status != jobScheduled {
		t.Errorf("status = %q, want %q", j.Status, jobScheduled)
	}
}

// after_acting decides whether a job survives a wake on which it acts. It is
// named for the moment it applies to, not for the schedule: an eval showed
// models reading a field called "repeat" as a question about cadence, and
// answering it from the request's own "check every two minutes".
func TestAfterActingDefaultsToContinueAndStopEndsTheJob(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")

	on, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "30m", Prompt: "look"})
	if err != nil {
		t.Fatal(err)
	}
	if on.AfterActing != afterContinue {
		t.Errorf("after_acting = %q, want %q by default", on.AfterActing, afterContinue)
	}

	off, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "2m", Check: "true",
		Prompt: "tell me", AfterActing: afterStop})
	if err != nil {
		t.Fatal(err)
	}
	if off.AfterActing != afterStop {
		t.Errorf("after_acting = %q, want %q", off.AfterActing, afterStop)
	}

	sched := NewScheduler(a)
	sched.reschedule(off, true)
	got, _ := a.store.Job(off.ID)
	if got == nil {
		t.Fatal("a finished job must be kept, so its log can still be read")
	}
	if got.Status != jobDone {
		t.Errorf("status = %q, want %q after acting", got.Status, jobDone)
	}
	sched.reschedule(on, true)
	still, _ := a.store.Job(on.ID)
	if still == nil || still.Status != jobScheduled {
		t.Error("a job set to continue must stay scheduled after acting")
	}
}

func TestAfterActingRejectsAnythingElse(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if _, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "30m",
		Prompt: "look", AfterActing: "sometimes"}); err == nil {
		t.Error("an unrecognised after_acting must be refused, not silently defaulted")
	}
}

// The cost floor is gone. It rejected the request the operator actually made
// and the agent routed around it: told it could not have a ten-minute stretch
// reminder, it created a one-shot whose prompt was "remind them, then schedule
// the next one" — the same cadence, the same model call per wake, plus a
// schedule call on top. A rule that is more expensive to enforce than to drop
// is not a cost rule.
func TestAnyCadenceIsAccepted(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	cases := []struct {
		name string
		spec JobSpec
	}{
		{"a ten-minute reminder, which is what people ask for", JobSpec{Schedule: "10m", Prompt: "stretch"}},
		{"three minutes, no check", JobSpec{Schedule: "3m", Prompt: "p"}},
		{"a check still costs nothing", JobSpec{Schedule: "3m", Check: "true", Prompt: "p"}},
		{"a daily cron", JobSpec{Schedule: "0 8 * * *", Prompt: "p"}},
		{"an instant", JobSpec{Schedule: time.Now().Add(time.Minute).Format(time.RFC3339), Prompt: "p"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.spec.SessionID = s.ID
			if _, err := a.CreateJob(c.spec); err != nil {
				t.Fatalf("want acceptance, got %v", err)
			}
		})
	}
}

func jobJSON(t *testing.T, a *App, id string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/jobs/"+id, nil))
	if w.Code != 200 {
		t.Fatalf("GET /jobs/%s = %d: %s", id, w.Code, w.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// "Remind me in two minutes" is a duration that stops after acting. It needs no
// clock and no absolute instant, and it never needed an exemption either.
func TestARelativeOneShotNeedsNoClock(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "2m",
		Prompt: "Stretch.", AfterActing: afterStop})
	if err != nil {
		t.Fatalf("a one-off two minutes out must be accepted: %v", err)
	}
	if j.AfterActing != afterStop {
		t.Error("after_acting must stay stop")
	}
	if d := time.Until(j.NextRunAt); d < 90*time.Second || d > 150*time.Second {
		t.Errorf("next wake in %v, want about two minutes", d)
	}
}
