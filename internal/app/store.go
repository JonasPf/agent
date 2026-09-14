package app

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	dir string
	db  *sql.DB

	mu       sync.RWMutex
	sessions map[string]*Session
	entries  map[string][]Entry // in-memory mirror of the transcripts
}

const schema = `
create table if not exists jobs (
  id text primary key, session_id text not null, schedule text not null,
  check_cmd text, prompt text not null,
  after_acting text not null default 'continue',
  status text not null default 'scheduled', run_count integer not null default 0,
  fail_count integer not null default 0, next_run_at text not null,
  last_status text, created_at text not null);
-- Every wake a job has had, so "has this been running?" is answered at the job
-- rather than by reading the conversation it fires into.
create table if not exists job_runs (
  id text primary key, job_id text not null, session_id text,
  at text not null, due_at text, outcome text not null, message text);
create index if not exists job_runs_by_job on job_runs(job_id, at);
create table if not exists memory (
  id text primary key, text text not null, source_session text, created_at text not null);
-- Web push went with the notification stack (ADR-026); dead letters and job
-- expiry went with the run log (ADR-028). Dropping the tables is how a removal
-- reaches a database that already exists.
drop table if exists push_subscriptions;
drop table if exists dead_letters;
create virtual table if not exists entry_fts using fts5(session_id, seq unindexed, body);
`

// migrate adds what "create table if not exists" cannot. A table that already
// exists is left exactly as it was, so a column added to the schema never
// reaches a database anyone is actually using — which is every database except
// the first one ever opened.
func migrate(db *sql.DB) error {
	cols, err := columns(db, "jobs")
	if err != nil {
		return err
	}
	if !cols["status"] {
		// A job written before there was a status has been waking all along.
		if _, err := db.Exec(`alter table jobs add column status text not null default 'scheduled'`); err != nil {
			return err
		}
	}
	return nil
}

func columns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`select name from pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// DBPath is where the database lives: a directory of its own inside the data
// directory, holding nothing but the database and the journals SQLite writes
// beside it.
//
// It is not in the data directory itself, and the reason is the sandbox. A tool
// is handed this database on purpose, and SQLite creates its write-ahead log and
// shared-memory file when it first needs them — which means permission to create
// a file in the directory holding it. The data directory holds every transcript,
// and Landlock grants a tree or it does not: there is no way to grant a
// directory while denying what is already inside it.
func DBPath(dataDir string) string {
	return filepath.Join(dataDir, "db", "agent.db")
}

// moveLegacyDatabase brings a database from before that split into place. It
// runs before the database is opened, so nothing is holding it, and it moves the
// journals with it: a write-ahead log separated from its database is worse than
// no log at all.
func moveLegacyDatabase(dataDir string) error {
	legacy := filepath.Join(dataDir, "agent.db")
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}
	if _, err := os.Stat(DBPath(dataDir)); err == nil {
		return nil // both exist: the newer one wins, and the old one is left to be looked at
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from, to := legacy+suffix, DBPath(dataDir)+suffix
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	return nil
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(DBPath(dir)), 0o755); err != nil {
		return nil, err
	}
	if err := moveLegacyDatabase(dir); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", DBPath(dir)+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, db: db, sessions: map[string]*Session{}, entries: map[string][]Entry{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) sessionDir(id string) string { return filepath.Join(s.dir, "sessions", id) }

// load rebuilds all session metadata and the search index from disk.
func (s *Store) load() error {
	ents, err := os.ReadDir(filepath.Join(s.dir, "sessions"))
	if err != nil {
		return err
	}
	var indexed int
	_ = s.db.QueryRow(`select count(*) from entry_fts`).Scan(&indexed)
	reindex := indexed == 0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		var sess Session
		b, err := os.ReadFile(filepath.Join(s.sessionDir(id), "meta.json"))
		if err != nil {
			continue
		}
		if err := json.Unmarshal(b, &sess); err != nil {
			continue
		}
		list, err := readTranscript(filepath.Join(s.sessionDir(id), "transcript.jsonl"))
		if err != nil {
			return err
		}
		s.sessions[id] = &sess
		s.entries[id] = list
		if reindex {
			for _, en := range list {
				s.index(id, en)
			}
		}
	}
	return nil
}

func readTranscript(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

func (s *Store) index(sessionID string, e Entry) {
	body := e.Text
	for _, tc := range e.ToolCalls {
		body += "\n" + tc.Name + " " + tc.Arguments
	}
	if len(e.ToolResult) > 0 {
		body += "\n" + string(e.ToolResult)
	}
	if strings.TrimSpace(body) == "" {
		return
	}
	_, _ = s.db.Exec(`insert into entry_fts(session_id, seq, body) values(?,?,?)`, sessionID, e.Seq, body)
}

// ---- sessions ----

func (s *Store) PutSession(sess *Session) error {
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return s.writeMeta(sess)
}

func (s *Store) writeMeta(sess *Session) error {
	if err := os.MkdirAll(s.sessionDir(sess.ID), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(sess, "", "  ")
	return os.WriteFile(filepath.Join(s.sessionDir(sess.ID), "meta.json"), b, 0o644)
}

func (s *Store) Session(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *Store) Sessions() []*Session {
	s.mu.RLock()
	out := make([]*Session, 0, len(s.sessions))
	for _, v := range s.sessions {
		out = append(out, v)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].LastActiveAt.After(out[j].LastActiveAt) })
	return out
}

func (s *Store) DeleteSession(id string) error {
	s.mu.Lock()
	delete(s.sessions, id)
	delete(s.entries, id)
	s.mu.Unlock()
	_, _ = s.db.Exec(`delete from entry_fts where session_id=?`, id)
	_, _ = s.db.Exec(`delete from jobs where session_id=?`, id)
	_, _ = s.db.Exec(`delete from job_runs where session_id=?`, id)
	return os.RemoveAll(s.sessionDir(id))
}

// Append writes an entry to the transcript. Entries are never modified once written.
func (s *Store) Append(sessionID string, e Entry) (Entry, error) {
	s.mu.Lock()
	list := s.entries[sessionID]
	e.Seq = len(list)
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	if e.Type == "" {
		e.Type = "message"
	}
	s.entries[sessionID] = append(list, e)
	sess := s.sessions[sessionID]
	if sess != nil {
		sess.LastActiveAt = e.CreatedAt
	}
	s.mu.Unlock()

	line, _ := json.Marshal(e)
	f, err := os.OpenFile(filepath.Join(s.sessionDir(sessionID), "transcript.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return e, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return e, err
	}
	s.index(sessionID, e)
	if sess != nil {
		_ = s.writeMeta(sess)
	}
	return e, nil
}

func (s *Store) Entries(sessionID string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, len(s.entries[sessionID]))
	copy(out, s.entries[sessionID])
	return out
}

type SearchHit struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Seq       int    `json:"seq"`
	Snippet   string `json:"snippet"`
}

// Search queries the full-text index. A sessionID scopes it to one conversation,
// which is what lets a session reach back below its own compactions; empty
// searches every session.
func (s *Store) Search(q, sessionID string, limit int) ([]SearchHit, error) {
	query := `select session_id, seq, snippet(entry_fts, 2, '[', ']', '…', 12) from entry_fts where entry_fts match ? limit ?`
	args := []any{q, limit}
	if sessionID != "" {
		query = `select session_id, seq, snippet(entry_fts, 2, '[', ']', '…', 12) from entry_fts where entry_fts match ? and session_id = ? limit ?`
		args = []any{q, sessionID, limit}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.SessionID, &h.Seq, &h.Snippet); err != nil {
			return nil, err
		}
		if sess := s.Session(h.SessionID); sess != nil {
			h.Title = sess.Title
		}
		out = append(out, h)
	}
	return out, nil
}

// ---- jobs ----

func (s *Store) PutJob(j *Job) error {
	_, err := s.db.Exec(`insert or replace into jobs
	 (id,session_id,schedule,check_cmd,prompt,after_acting,status,run_count,fail_count,next_run_at,last_status,created_at)
	 values(?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.SessionID, j.Schedule, j.Check, j.Prompt,
		j.AfterActing, j.Status, j.RunCount, j.FailCount, j.NextRunAt.Format(time.RFC3339), j.LastStatus,
		j.CreatedAt.Format(time.RFC3339))
	return err
}

func scanJobs(rows *sql.Rows) ([]*Job, error) {
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		var j Job
		var check, last, status sql.NullString
		var next, created string
		if err := rows.Scan(&j.ID, &j.SessionID, &j.Schedule, &check, &j.Prompt,
			&j.AfterActing, &status, &j.RunCount, &j.FailCount, &next, &last, &created); err != nil {
			return nil, err
		}
		j.Check, j.LastStatus = check.String, last.String
		j.Status = status.String
		if j.Status == "" {
			j.Status = jobScheduled
		}
		j.NextRunAt, _ = time.Parse(time.RFC3339, next)
		j.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, &j)
	}
	return out, rows.Err()
}

const jobCols = `id,session_id,schedule,check_cmd,prompt,after_acting,status,run_count,fail_count,next_run_at,last_status,created_at`

func formatOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func (s *Store) Jobs() ([]*Job, error) {
	rows, err := s.db.Query(`select ` + jobCols + ` from jobs order by next_run_at`)
	if err != nil {
		return nil, err
	}
	return scanJobs(rows)
}

func (s *Store) Job(id string) (*Job, error) {
	rows, err := s.db.Query(`select `+jobCols+` from jobs where id=?`, id)
	if err != nil {
		return nil, err
	}
	js, err := scanJobs(rows)
	if err != nil || len(js) == 0 {
		return nil, err
	}
	return js[0], nil
}

func (s *Store) SessionJobs(sessionID string) ([]*Job, error) {
	rows, err := s.db.Query(`select `+jobCols+` from jobs where session_id=?`, sessionID)
	if err != nil {
		return nil, err
	}
	return scanJobs(rows)
}

// DeleteJob takes the job's log with it. A log with no job is unreachable
// from anywhere in the interface, which makes it storage rather than a record.
func (s *Store) DeleteJob(id string) error {
	if _, err := s.db.Exec(`delete from job_runs where job_id=?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`delete from jobs where id=?`, id)
	return err
}

// DeleteJobsByStatus removes every job in one state, optionally within one
// session, and the runs that belong to them. It is the only delete that acts on
// more than one job at a time, which is why the state is a parameter and not a
// default: nothing here can reach a job that still has work in it unless the
// caller names the state it means.
func (s *Store) DeleteJobsByStatus(status, sessionID string) (int, error) {
	where := `status=?`
	args := []any{status}
	if sessionID != "" {
		where += ` and session_id=?`
		args = append(args, sessionID)
	}
	if _, err := s.db.Exec(`delete from job_runs where job_id in (select id from jobs where `+where+`)`, args...); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`delete from jobs where `+where, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (s *Store) MoveJobs(from, to string) error {
	_, err := s.db.Exec(`update jobs set session_id=? where session_id=?`, to, from)
	return err
}

// ---- memory ----

func (s *Store) Memory() ([]MemoryItem, error) {
	rows, err := s.db.Query(`select id,text,source_session,created_at from memory order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryItem
	for rows.Next() {
		var m MemoryItem
		var created string
		var src sql.NullString
		if err := rows.Scan(&m.ID, &m.Text, &src, &created); err != nil {
			return nil, err
		}
		m.SourceSession = src.String
		m.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) PutMemory(m MemoryItem) error {
	_, err := s.db.Exec(`insert or replace into memory(id,text,source_session,created_at) values(?,?,?,?)`,
		m.ID, m.Text, m.SourceSession, m.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) DeleteMemory(id string) error {
	_, err := s.db.Exec(`delete from memory where id=?`, id)
	return err
}

// ---- job runs ----

func (s *Store) PutJobRun(r JobRun) error {
	if r.ID == "" {
		r.ID = newID()
	}
	_, err := s.db.Exec(`insert or replace into job_runs(id,job_id,session_id,at,due_at,outcome,message)
	 values(?,?,?,?,?,?,?)`,
		r.ID, r.JobID, r.SessionID, r.At.Format(time.RFC3339), formatOrEmpty(r.DueAt),
		r.Outcome, r.Message)
	return err
}

// JobRuns returns a job's wakes, newest first.
func (s *Store) JobRuns(jobID string) ([]JobRun, error) {
	rows, err := s.db.Query(`select id,job_id,session_id,at,due_at,outcome,message
	 from job_runs where job_id=? order by at desc, rowid desc`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobRun{}
	for rows.Next() {
		var r JobRun
		var sess, due, msg sql.NullString
		var at string
		if err := rows.Scan(&r.ID, &r.JobID, &sess, &at, &due, &r.Outcome, &msg); err != nil {
			return nil, err
		}
		r.SessionID, r.Message = sess.String, msg.String
		r.At, _ = time.Parse(time.RFC3339, at)
		if due.String != "" {
			r.DueAt, _ = time.Parse(time.RFC3339, due.String)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteJobRuns(jobID string) error {
	_, err := s.db.Exec(`delete from job_runs where job_id=?`, jobID)
	return err
}

func (s *Store) DB() *sql.DB { return s.db }

// ImportSession writes a session that arrived from an archive: its metadata, its
// transcript, and its place in the search index. Everything derived is rebuilt
// here, so an imported conversation is indistinguishable from one that was held.
func (s *Store) ImportSession(sess *Session, entries []Entry) error {
	if err := os.MkdirAll(s.sessionDir(sess.ID), 0o755); err != nil {
		return err
	}
	var body strings.Builder
	for _, e := range entries {
		line, _ := json.Marshal(e)
		body.Write(line)
		body.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(s.sessionDir(sess.ID), "transcript.jsonl"),
		[]byte(body.String()), 0o644); err != nil {
		return err
	}
	if err := s.writeMeta(sess); err != nil {
		return err
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.entries[sess.ID] = entries
	s.mu.Unlock()
	_, _ = s.db.Exec(`delete from entry_fts where session_id=?`, sess.ID)
	for _, e := range entries {
		s.index(sess.ID, e)
	}
	return nil
}
