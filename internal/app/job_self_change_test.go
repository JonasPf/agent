package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// dueJob creates a job in a conversation and makes it due now.
func dueJob(t *testing.T, a *App, sessionID, schedule string) *Job {
	t.Helper()
	j, err := a.CreateJob(JobSpec{SessionID: sessionID, Schedule: schedule,
		Prompt: "Check the oven.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}
	return j
}

// A job ends the way the spec says it does: its own wake deletes it with the
// schedule tool. The wake then finished and wrote the job back as it had been
// when the wake began, so the job the agent had just deleted was scheduled
// again, and woke again.
func TestAJobDeletedDuringItsOwnWakeStaysDeleted(t *testing.T) {
	srv := scriptedModel(t,
		toolStep{tool: "schedule", args: `{"action":"delete"}`},
		toolStep{text: "The oven is off, so this reminder is over."})
	a := metricsApp(t, t.TempDir(), srv)
	a.sched = NewScheduler(a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j := dueJob(t, a, s.ID, "20m")

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	deleted := false
	for _, e := range a.store.Entries(s.ID) {
		if e.Role == "tool" && e.ToolName == "schedule" {
			var res toolResult
			_ = json.Unmarshal(e.ToolResult, &res)
			deleted = res.OK
		}
	}
	if !deleted {
		t.Fatalf("the wake did not delete its job, so the test shows nothing: %+v", a.store.Entries(s.ID))
	}
	if got, err := a.store.Job(j.ID); err != nil || got != nil {
		t.Errorf("the job its own wake deleted is back: %+v (%v)", got, err)
	}
	if runs := runsOf(t, a, j.ID); len(runs) != 0 {
		t.Errorf("a deleted job was left %d runs nothing can show", len(runs))
	}
}

// A job's wake may change the job too — the agent moving its own reminder to
// another cadence. The same write took that back: the wake ended and stored
// the schedule it had started with.
func TestAJobEditedDuringItsOwnWakeKeepsTheEdit(t *testing.T) {
	a := metricsApp(t, t.TempDir(), nil)
	a.sched = NewScheduler(a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j := dueJob(t, a, s.ID, "20m")
	srv := scriptedModel(t,
		toolStep{tool: "schedule", args: `{"action":"edit","id":"` + j.ID + `","schedule":"6h","prompt":"Check the fridge."}`},
		toolStep{text: "Moved to every six hours."})
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	got, err := a.store.Job(j.ID)
	if err != nil || got == nil {
		t.Fatalf("the job is gone: %v", err)
	}
	if got.Schedule != "6h" || got.Prompt != "Check the fridge." {
		t.Errorf("the job is back to schedule %q, prompt %q; its wake set 6h and the fridge", got.Schedule, got.Prompt)
	}
	if wait := time.Until(got.NextRunAt); wait < 5*time.Hour {
		t.Errorf("the next wake is in %v, on the schedule the wake replaced", wait.Round(time.Minute))
	}
	if got.RunCount != 1 {
		t.Errorf("run count = %d, want the wake counted", got.RunCount)
	}
	if runs := runsOf(t, a, j.ID); len(runs) != 1 || runs[0].Outcome != jobFired {
		t.Errorf("runs = %+v, want the one wake logged as fired", runs)
	}
}
