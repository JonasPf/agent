package app

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
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
  check_cmd text, prompt text not null, expires_at text not null,
  on_condition_met text not null, run_count integer not null default 0,
  fail_count integer not null default 0, next_run_at text not null,
  last_status text, created_at text not null);
create table if not exists dead_letters (
  id text primary key, job_id text, session_id text, spec text not null,
  reason text not null, detail text, run_count integer, status text not null,
  created_at text not null);
create table if not exists memory (
  id text primary key, text text not null, source_session text, created_at text not null);
create table if not exists push_subscriptions (
  endpoint text primary key, p256dh text, auth text, created_at text not null);
create virtual table if not exists entry_fts using fts5(session_id, seq unindexed, body);
`

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "agent.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
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
	_, _ = s.db.Exec(`delete from dead_letters where session_id=?`, id)
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

func (s *Store) Search(q string, limit int) ([]SearchHit, error) {
	rows, err := s.db.Query(
		`select session_id, seq, snippet(entry_fts, 2, '[', ']', '…', 12) from entry_fts where entry_fts match ? limit ?`,
		q, limit)
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
	 (id,session_id,schedule,check_cmd,prompt,expires_at,on_condition_met,run_count,fail_count,next_run_at,last_status,created_at)
	 values(?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.SessionID, j.Schedule, j.Check, j.Prompt, j.ExpiresAt.Format(time.RFC3339),
		j.OnConditionMet, j.RunCount, j.FailCount, j.NextRunAt.Format(time.RFC3339), j.LastStatus,
		j.CreatedAt.Format(time.RFC3339))
	return err
}

func scanJobs(rows *sql.Rows) ([]*Job, error) {
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		var j Job
		var check, last sql.NullString
		var exp, next, created string
		if err := rows.Scan(&j.ID, &j.SessionID, &j.Schedule, &check, &j.Prompt, &exp,
			&j.OnConditionMet, &j.RunCount, &j.FailCount, &next, &last, &created); err != nil {
			return nil, err
		}
		j.Check, j.LastStatus = check.String, last.String
		j.ExpiresAt, _ = time.Parse(time.RFC3339, exp)
		j.NextRunAt, _ = time.Parse(time.RFC3339, next)
		j.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, &j)
	}
	return out, rows.Err()
}

const jobCols = `id,session_id,schedule,check_cmd,prompt,expires_at,on_condition_met,run_count,fail_count,next_run_at,last_status,created_at`

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

func (s *Store) DeleteJob(id string) error {
	_, err := s.db.Exec(`delete from jobs where id=?`, id)
	return err
}

func (s *Store) MoveJobs(from, to string) error {
	_, err := s.db.Exec(`update jobs set session_id=? where session_id=?`, to, from)
	return err
}

// ---- dead letters ----

func (s *Store) PutDeadLetter(d *DeadLetter) error {
	spec, _ := json.Marshal(d.JobSpec)
	_, err := s.db.Exec(`insert or replace into dead_letters
	 (id,job_id,session_id,spec,reason,detail,run_count,status,created_at) values(?,?,?,?,?,?,?,?,?)`,
		d.ID, d.JobID, d.SessionID, string(spec), d.Reason, d.Detail, d.RunCount, d.Status,
		d.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) DeadLetters() ([]*DeadLetter, error) {
	rows, err := s.db.Query(`select id,job_id,session_id,spec,reason,detail,run_count,status,created_at
	 from dead_letters order by created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DeadLetter
	for rows.Next() {
		var d DeadLetter
		var spec, created string
		var detail sql.NullString
		if err := rows.Scan(&d.ID, &d.JobID, &d.SessionID, &spec, &d.Reason, &detail, &d.RunCount,
			&d.Status, &created); err != nil {
			return nil, err
		}
		d.Detail = detail.String
		d.CreatedAt, _ = time.Parse(time.RFC3339, created)
		_ = json.Unmarshal([]byte(spec), &d.JobSpec)
		out = append(out, &d)
	}
	return out, rows.Err()
}

func (s *Store) OpenDeadLetterFor(jobID string) bool {
	var n int
	_ = s.db.QueryRow(`select count(*) from dead_letters where job_id=? and status='open'`, jobID).Scan(&n)
	return n > 0
}

func (s *Store) CloseDeadLetter(id string) error {
	_, err := s.db.Exec(`update dead_letters set status='closed' where id=?`, id)
	return err
}

func (s *Store) DeadLetter(id string) (*DeadLetter, error) {
	all, err := s.DeadLetters()
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, fmt.Errorf("dead letter %s not found", id)
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

// ---- push ----

type PushSub struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

func (s *Store) PutPushSub(p PushSub) error {
	_, err := s.db.Exec(`insert or replace into push_subscriptions(endpoint,p256dh,auth,created_at) values(?,?,?,?)`,
		p.Endpoint, p.P256dh, p.Auth, time.Now().Format(time.RFC3339))
	return err
}

func (s *Store) PushSubs() ([]PushSub, error) {
	rows, err := s.db.Query(`select endpoint,p256dh,auth from push_subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSub
	for rows.Next() {
		var p PushSub
		if err := rows.Scan(&p.Endpoint, &p.P256dh, &p.Auth); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeletePushSub(endpoint string) {
	_, _ = s.db.Exec(`delete from push_subscriptions where endpoint=?`, endpoint)
}

func (s *Store) DB() *sql.DB { return s.db }
