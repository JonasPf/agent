package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

// settle waits for a session's queue to drain, so a test can assert on what a
// tick produced rather than on when it produced it.
func settle(t *testing.T, a *App, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !a.Busy(sessionID) {
			time.Sleep(20 * time.Millisecond)
			if !a.Busy(sessionID) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the session never went idle")
}

func wakes(a *App, sessionID string) int {
	n := 0
	for _, e := range a.store.Entries(sessionID) {
		if e.Type == "message" && e.Role == "user" && e.JobID != "" {
			n++
		}
	}
	return n
}

// A host that sleeps takes the scheduler with it. Nothing catches up on the
// missed occurrences: the tick compares against the wall clock, so everything
// overdue is seen at once, and a repeating job fires a single time however long
// the gap was. Nine missed stretch reminders do not arrive together.
func TestSleepingThroughSeveralWakesFiresOnce(t *testing.T) {
	a, _ := modelBackedApp(t, "Reminding you.")
	a.sched = NewScheduler(a)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Remind me to stretch.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}

	// Three hours of sleep: nine wakes came due while nothing was running.
	j.NextRunAt = time.Now().Add(-3 * time.Hour)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	if got := wakes(a, s.ID); got != 1 {
		t.Errorf("the job fired %d times, want 1", got)
	}
	after, err := a.store.Job(j.ID)
	if err != nil || after == nil {
		t.Fatalf("the job is gone: %v", err)
	}
	// The next wake is measured from now, not from the one that was missed —
	// which is what stops it walking forward through the backlog.
	if d := time.Until(after.NextRunAt); d < 19*time.Minute || d > 21*time.Minute {
		t.Errorf("next wake in %s, want about 20m from now", d.Round(time.Second))
	}
}

// A wake missed while the host slept is still run when it comes back. There is
// no deadline to pass and nothing to give up on: the job stays, and its log
// says when it actually ran.
func TestAWakeMissedDuringASleepStillRuns(t *testing.T) {
	a, _ := modelBackedApp(t, "Reminding you.")
	a.sched = NewScheduler(a)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Tell me about the thing.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-2 * time.Hour)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	if got := wakes(a, s.ID); got != 1 {
		t.Errorf("the job fired %d times, want 1", got)
	}
	still, err := a.store.Job(j.ID)
	if err != nil || still == nil {
		t.Fatal("a job that slept through a wake was not kept")
	}
	runs, err := a.store.JobRuns(j.ID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("got %d runs, want 1: %v", len(runs), err)
	}
}

// The due time has to survive the whole path — job row, scheduler, turn, entry,
// projection — or the marker is only ever right in a unit test.
func TestAWakeDelayedByASleepingHostTellsTheModelSo(t *testing.T) {
	a, _ := modelBackedApp(t, "Reminding you.")
	a.sched = NewScheduler(a)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Remind me to stretch.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-90 * time.Minute)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	var wake Entry
	for _, e := range a.store.Entries(s.ID) {
		if e.Type == "message" && e.Role == "user" && e.JobID == j.ID {
			wake = e
		}
	}
	if wake.DueAt.IsZero() {
		t.Fatal("the wake was stored with no due time")
	}
	msgs := Project([]Entry{wake})
	got, _ := msgs[0].Content.(string)
	if !strings.Contains(got, "late") || !strings.Contains(got, "1h30m") {
		t.Errorf("the model was told %q, want it to say the wake was 1h30m late", got)
	}
}

// A wake that ran on time reaches the model with no mention of the clock.
func TestAPunctualWakeReachesTheModelUnqualified(t *testing.T) {
	a, _ := modelBackedApp(t, "Reminding you.")
	a.sched = NewScheduler(a)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Remind me to stretch.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	for _, e := range a.store.Entries(s.ID) {
		if e.Type != "message" || e.Role != "user" || e.JobID != j.ID {
			continue
		}
		got, _ := Project([]Entry{e})[0].Content.(string)
		if strings.Contains(got, "late") {
			t.Errorf("the model was told %q, want no mention of lateness", got)
		}
	}
}
