package app

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// create table if not exists does nothing to a table that already exists, so a
// new column never reaches a database that predates it. Every operator has one:
// the schema has to be migrated, not merely declared.
func TestAnExistingDatabaseGainsTheNewJobColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.db")

	// The schema as it stood before the run log: an until column, no status.
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`create table jobs (
	  id text primary key, session_id text not null, schedule text not null,
	  check_cmd text, prompt text not null, until text,
	  after_acting text not null default 'continue', run_count integer not null default 0,
	  fail_count integer not null default 0, next_run_at text not null,
	  last_status text, created_at text not null);
	 insert into jobs(id,session_id,schedule,prompt,until,after_acting,next_run_at,created_at)
	  values('J1','S1','20m','stretch','2026-01-01T00:00:00Z','continue','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("opening a database from before the run log failed: %v", err)
	}
	t.Cleanup(func() { _ = st.DB().Close() })

	jobs, err := st.Jobs()
	if err != nil {
		t.Fatalf("reading jobs failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("got %d jobs, want the one already there", len(jobs))
	}
	// A job written before there was a status is scheduled, not blank: it has
	// been waking all along.
	if jobs[0].Status != jobScheduled {
		t.Errorf("status = %q, want %q", jobs[0].Status, jobScheduled)
	}

	jobs[0].RunCount = 3
	if err := st.PutJob(jobs[0]); err != nil {
		t.Fatalf("writing a job failed: %v", err)
	}
	if err := st.PutJobRun(JobRun{JobID: "J1", At: time.Now(), Outcome: jobFired,
		Message: "hello"}); err != nil {
		t.Fatalf("writing a run failed: %v", err)
	}
	if runs, err := st.JobRuns("J1"); err != nil || len(runs) != 1 {
		t.Fatalf("got %d runs (%v), want 1", len(runs), err)
	}
}
