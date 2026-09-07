package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// A finished job is kept, because its log is the record of what it did. Kept
// long enough, though, the finished ones bury the scheduled ones: a session that
// reminds every ten minutes leaves a row per reminder. Clearing them is one
// action rather than one tap per row, and it can only ever reach jobs that have
// nothing left to do.

func doneJob(t *testing.T, a *App, sessionID, prompt string) *Job {
	t.Helper()
	j, err := a.CreateJob(JobSpec{SessionID: sessionID, Schedule: "30m", Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	j.Status = jobDone
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}
	return j
}

func clear(t *testing.T, a *App, query string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("DELETE", "/jobs"+query, nil))
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func TestFinishedJobsAreClearedInOneAction(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	one := doneJob(t, a, s.ID, "first reminder")
	two := doneJob(t, a, s.ID, "second reminder")
	live, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "30m", Prompt: "still watching"})
	if err != nil {
		t.Fatal(err)
	}

	code, body := clear(t, a, "?status=done")
	if code != 200 {
		t.Fatalf("DELETE /jobs?status=done = %d, want 200", code)
	}
	if n, _ := body["deleted"].(float64); int(n) != 2 {
		t.Errorf("deleted = %v, want 2", body["deleted"])
	}
	for _, gone := range []*Job{one, two} {
		if got, _ := a.store.Job(gone.ID); got != nil {
			t.Errorf("job %q survived the clear", gone.Prompt)
		}
	}
	if got, _ := a.store.Job(live.ID); got == nil {
		t.Fatal("a scheduled job was deleted along with the finished ones")
	}
}

// The log of a job goes with the job: leaving its runs behind would leave rows
// nothing can reach.
func TestClearingAFinishedJobTakesItsRunsWithIt(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j := doneJob(t, a, s.ID, "reminder")
	if err := a.store.PutJobRun(JobRun{JobID: j.ID, SessionID: s.ID, Outcome: jobFired, Message: "reminded"}); err != nil {
		t.Fatal(err)
	}

	if code, _ := clear(t, a, "?status=done"); code != 200 {
		t.Fatalf("clear = %d, want 200", code)
	}
	runs, err := a.store.JobRuns(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("%d runs left behind by the clear", len(runs))
	}
}

// The jobs screen can be opened for one conversation, and clearing from there
// must not reach into another's.
func TestAClearScopedToASessionLeavesOtherSessionsAlone(t *testing.T) {
	a := newTestApp(t)
	mine, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	theirs, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	here := doneJob(t, a, mine.ID, "here")
	there := doneJob(t, a, theirs.ID, "there")

	code, body := clear(t, a, "?status=done&session_id="+mine.ID)
	if code != 200 {
		t.Fatalf("clear = %d, want 200", code)
	}
	if n, _ := body["deleted"].(float64); int(n) != 1 {
		t.Errorf("deleted = %v, want 1", body["deleted"])
	}
	if got, _ := a.store.Job(here.ID); got != nil {
		t.Error("the finished job in this session survived")
	}
	if got, _ := a.store.Job(there.ID); got == nil {
		t.Error("a clear scoped to one session deleted another session's job")
	}
}

// Nothing may delete a job that still has work in it, so the status is named by
// the caller rather than assumed. A bare delete of the collection is refused.
func TestAClearMustNameTheStatusItDeletes(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	live, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "30m", Prompt: "still watching"})
	if err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"", "?status=", "?status=scheduled", "?session_id=" + s.ID} {
		if code, _ := clear(t, a, q); code != 400 {
			t.Errorf("DELETE /jobs%s = %d, want 400", q, code)
		}
	}
	if got, _ := a.store.Job(live.ID); got == nil {
		t.Fatal("a refused clear deleted a job anyway")
	}
}

// Clearing nothing is not an error: the button is offered while the list still
// holds finished jobs, and two taps in a row must not fail the second time.
func TestClearingWithNothingFinishedSucceedsAndDeletesNothing(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if _, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "30m", Prompt: "watching"}); err != nil {
		t.Fatal(err)
	}
	code, body := clear(t, a, "?status=done")
	if code != 200 {
		t.Fatalf("clear = %d, want 200", code)
	}
	if n, _ := body["deleted"].(float64); int(n) != 0 {
		t.Errorf("deleted = %v, want 0", body["deleted"])
	}
}
