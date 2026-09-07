package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func runsOf(t *testing.T, a *App, jobID string) []JobRun {
	t.Helper()
	runs, err := a.store.JobRuns(jobID)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

// A job's own site is where the operator checks what it has been doing. Without
// a log they had to read the session it fires into, and a job that moved across
// a rotation left its history in a session it no longer belongs to.
func TestAWakeThatActedIsLoggedWithItsTimeAndMessage(t *testing.T) {
	a, _ := modelBackedApp(t, "Reminder: feed the cat.")
	a.sched = NewScheduler(a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Remind me to feed the cat.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	runs := runsOf(t, a, j.ID)
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Outcome != "fired" {
		t.Errorf("outcome = %q, want fired", runs[0].Outcome)
	}
	if runs[0].At.IsZero() {
		t.Error("the run has no time")
	}
	if !strings.Contains(runs[0].Message, "feed the cat") {
		t.Errorf("message = %q, want what the agent said", runs[0].Message)
	}
}

// A wake whose check said no costs nothing and says nothing in the transcript.
// It is still the answer to "has this been running?", so it is logged too.
func TestAWakeThatDidNotActIsLogged(t *testing.T) {
	a, _ := modelBackedApp(t, "unused")
	a.sched = NewScheduler(a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m", Check: "false",
		Prompt: "Tell me when it happens.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	runs := runsOf(t, a, j.ID)
	if len(runs) != 1 || runs[0].Outcome != "skipped" {
		t.Fatalf("got %+v, want one skipped run", runs)
	}
}

// A failure keeps the job. Deleting it would take the log with it, which is the
// one record explaining why it stopped working.
func TestAFailedWakeIsLoggedAndTheJobSurvives(t *testing.T) {
	a := newTestApp(t)
	a.sched = NewScheduler(a)
	srv, _ := flakyModel(t, "never reached")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	a.or.retryBase = 0
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
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

	runs := runsOf(t, a, j.ID)
	if len(runs) != 1 || runs[0].Outcome != "failed" {
		t.Fatalf("got %+v, want one failed run", runs)
	}
	if runs[0].Message == "" {
		t.Error("a failed run must say what went wrong")
	}
	still, err := a.store.Job(j.ID)
	if err != nil || still == nil {
		t.Fatalf("the job was deleted on failure: %v", err)
	}
	if still.Status != jobScheduled {
		t.Errorf("status = %q, want %q — a failing job keeps trying", still.Status, jobScheduled)
	}
}

// A one-shot used to vanish the moment it fired, taking its record with it.
// "Remind me in two minutes" now leaves something to look at afterwards.
func TestAOneShotIsKeptAndMarkedDone(t *testing.T) {
	a, _ := modelBackedApp(t, "Reminder: feed the cat.")
	a.sched = NewScheduler(a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "2m",
		Prompt: "Remind me to feed the cat.", AfterActing: afterStop})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	done, err := a.store.Job(j.ID)
	if err != nil || done == nil {
		t.Fatalf("the finished job is gone: %v", err)
	}
	if done.Status != jobDone {
		t.Errorf("status = %q, want %q", done.Status, jobDone)
	}
	if len(runsOf(t, a, j.ID)) != 1 {
		t.Error("the run that finished it was not logged")
	}

	// And it does not wake again.
	a.sched.tick(context.Background())
	settle(t, a, s.ID)
	if n := len(runsOf(t, a, j.ID)); n != 1 {
		t.Errorf("a finished job ran again: %d runs", n)
	}
}

// The log is per job and outlives the session a job was created in, because a
// job follows its conversation across a rotation.
func TestTheLogBelongsToTheJobNotTheSession(t *testing.T) {
	a, _ := modelBackedApp(t, "ok")
	a.sched = NewScheduler(a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j, _ := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m", Prompt: "p",
		AfterActing: afterContinue})
	other, _ := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m", Prompt: "q",
		AfterActing: afterContinue})

	if err := a.store.PutJobRun(JobRun{JobID: j.ID, SessionID: s.ID,
		At: time.Now(), Outcome: "fired", Message: "mine"}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.PutJobRun(JobRun{JobID: other.ID, SessionID: s.ID,
		At: time.Now(), Outcome: "fired", Message: "theirs"}); err != nil {
		t.Fatal(err)
	}
	runs := runsOf(t, a, j.ID)
	if len(runs) != 1 || runs[0].Message != "mine" {
		t.Fatalf("got %+v, want only this job's run", runs)
	}
}

// Deleting a job takes its log with it: nothing should outlive the row it
// describes and sit unreachable in the database.
func TestDeletingAJobDeletesItsLog(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j, _ := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m", Prompt: "p",
		AfterActing: afterContinue})
	if err := a.store.PutJobRun(JobRun{JobID: j.ID, SessionID: s.ID,
		At: time.Now(), Outcome: "fired", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeleteJob(j.ID); err != nil {
		t.Fatal(err)
	}
	if runs := runsOf(t, a, j.ID); len(runs) != 0 {
		t.Errorf("got %d runs after deleting the job, want 0", len(runs))
	}
}
