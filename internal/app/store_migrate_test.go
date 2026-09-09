package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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

// The database sits in a directory of its own, and the reason is the sandbox.
// SQLite creates its write-ahead log and shared-memory file beside the database
// when it first needs them, which needs permission to create a file in that
// directory — and the directory it used to sit in is the one holding every
// transcript. Landlock grants a tree or it does not; it has no way to grant a
// directory while denying what is already in it.
func TestTheDatabaseMovesOutOfTheTranscriptDirectory(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "agent.db")

	// A store from before the move: the file where it used to be, with a row in
	// it that has to survive.
	old, err := sql.Open("sqlite", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`create table notes_items (id integer primary key, text text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`insert into notes_items (text) values ('kept')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB().Close()

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("the database is still in the transcript directory")
	}
	if _, err := os.Stat(DBPath(dir)); err != nil {
		t.Fatalf("the database is not where it should be: %v", err)
	}
	var text string
	if err := st.DB().QueryRow(`select text from notes_items`).Scan(&text); err != nil {
		t.Fatalf("the move lost the contents: %v", err)
	}
	if text != "kept" {
		t.Errorf("text = %q, want %q", text, "kept")
	}
}

// A tool is handed the database on purpose, so what it is handed must be the
// directory holding it and nothing above that.
func TestTheDatabaseDirectoryHoldsOnlyTheDatabase(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB().Close()

	if filepath.Dir(DBPath(dir)) == filepath.Clean(dir) {
		t.Fatal("the database is in the data directory itself, which holds every transcript")
	}
	entries, err := os.ReadDir(filepath.Dir(DBPath(dir)))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "agent.db") {
			t.Errorf("%s is in the database directory; only the database and its journals belong there", e.Name())
		}
	}
}
