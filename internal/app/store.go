package app

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

type Store struct {
	dir string
	db  *sql.DB

	mu       sync.RWMutex
	sessions map[string]*Session
	entries  map[string][]Entry // in-memory mirror of the transcripts
	// deleted holds the sessions deleted since the store opened. A turn or a
	// job can still be finishing when its session goes, and what it writes
	// afterwards is refused here rather than recreating the session under its
	// old identifier.
	deleted map[string]bool
	// metaMu puts writes of meta.json in order, so the file always ends up
	// holding the newest session published.
	metaMu sync.Mutex
	// writeMu puts appends to transcripts in order: a number is handed out and
	// written in one step, so the file holds entries in the order they are
	// numbered, and a write that fails can be taken back.
	writeMu sync.Mutex
}

// errSessionDeleted is a write that arrived after its session was deleted.
var errSessionDeleted = errors.New("the session was deleted")

func (s *Store) wasDeleted(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deleted[id]
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
-- Web push went with the notification stack (ADR-026); dead letters and job
-- expiry went with the run log (ADR-028). Dropping the tables is how a removal
-- reaches a database that already exists. Push came back with ADR-066, but not
-- here: every tool is handed this database, so its keys live in push.json.
drop table if exists push_subscriptions;
drop table if exists dead_letters;
-- Memory went with ADR-055: nothing crosses a session boundary on its own any
-- more, so the store that did is dropped rather than left behind unread.
drop table if exists memory;
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
	s := &Store{dir: dir, db: db, sessions: map[string]*Session{}, entries: map[string][]Entry{},
		deleted: map[string]bool{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) sessionDir(id string) string { return filepath.Join(s.dir, "sessions", id) }

// load rebuilds all session metadata and the search index from disk.
//
// Startup waits on it, and it reads and decodes every transcript the agent has
// ever kept. Each session is independent of every other, so they are read in
// parallel, one reader per processor.
func (s *Store) load() error {
	ents, err := os.ReadDir(filepath.Join(s.dir, "sessions"))
	if err != nil {
		return err
	}
	var indexed int
	_ = s.db.QueryRow(`select count(*) from entry_fts`).Scan(&indexed)

	type loaded struct {
		id   string
		sess *Session
		list []Entry
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		out      []loaded
		firstErr error
	)
	ids := make(chan string)
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ids {
				sess, list, err := s.loadSession(id)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				if sess != nil {
					out = append(out, loaded{id, sess, list})
				}
				mu.Unlock()
			}
		}()
	}
	for _, e := range ents {
		if e.IsDir() {
			ids <- e.Name()
		}
	}
	close(ids)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	for _, l := range out {
		s.sessions[l.id] = l.sess
		s.entries[l.id] = l.list
	}
	if indexed == 0 {
		all := make(map[string][]Entry, len(out))
		for _, l := range out {
			all[l.id] = l.list
		}
		return s.indexAll(all)
	}
	return nil
}

// loadSession reads one session's metadata and transcript. A session whose
// metadata cannot be read or decoded is skipped, as it always was; a transcript
// that cannot be read is an error.
func (s *Store) loadSession(id string) (*Session, []Entry, error) {
	var sess Session
	b, err := os.ReadFile(filepath.Join(s.sessionDir(id), "meta.json"))
	if err != nil {
		return nil, nil, nil
	}
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, nil, nil
	}
	list, err := readTranscript(filepath.Join(s.sessionDir(id), "transcript.jsonl"))
	if err != nil {
		return nil, nil, err
	}
	if !hasKey(b, "read_through") {
		sess.ReadThrough = readThroughFromCount(list, sess.Unread)
	}
	return &sess, list, nil
}

// hasKey says whether a stored object names a field at all, which a zero value
// cannot: a session read through nothing and one written before there was a
// read point both decode to 0.
func hasKey(b []byte, key string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// readThroughFromCount places the read point of a session that stored a count
// instead: just before its last n assistant messages, so it keeps exactly the
// count it had.
func readThroughFromCount(entries []Entry, n int) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if !countsAsUnread(entries[i]) {
			continue
		}
		if n == 0 {
			return entries[i].Seq
		}
		n--
	}
	// Every message is unread, or there are fewer than it counted.
	return 0
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
	sc.Buffer(make([]byte, 64<<10), 1<<26)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// indexBody is what the search index holds for an entry, or "" for one with
// nothing to find.
func indexBody(e Entry) string {
	body := e.Text
	for _, tc := range e.ToolCalls {
		body += "\n" + tc.Name + " " + tc.Arguments
	}
	if len(e.ToolResult) > 0 {
		body += "\n" + string(e.ToolResult)
	}
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return body
}

func (s *Store) index(sessionID string, e Entry) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.deleted[sessionID] {
		return
	}
	if body := indexBody(e); body != "" {
		_, _ = s.db.Exec(`insert into entry_fts(session_id, seq, body) values(?,?,?)`, sessionID, e.Seq, body)
	}
}

// indexAll writes many entries into the search index in one transaction. A
// commit of its own for each entry is a sync to disk for each entry, and
// rebuilding the index of a few hundred conversations that way took seconds.
func (s *Store) indexAll(sessions map[string][]Entry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`insert into entry_fts(session_id, seq, body) values(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for id, list := range sessions {
		for _, e := range list {
			if body := indexBody(e); body != "" {
				if _, err := stmt.Exec(id, e.Seq, body); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

// ---- sessions ----

// A session the store has published is never changed again. A turn counting
// its cost, the operator renaming it, a page reading it through, and every page
// listing it all touch it at once; changed in place, each read the others'
// writes half made. So a change copies the session under the lock and
// publishes the copy, and whoever holds a session holds one nothing changes
// under it.

// PutSession publishes a copy of sess, which stays the caller's. It is how a
// session is first published; a session already published is changed with
// UpdateSession, which cannot lose a change made at the same time.
func (s *Store) PutSession(sess *Session) error {
	c := *sess
	s.mu.Lock()
	if s.deleted[c.ID] {
		s.mu.Unlock()
		return errSessionDeleted
	}
	s.sessions[c.ID] = &c
	s.mu.Unlock()
	return s.flushMeta(c.ID)
}

// UpdateSession changes a session by applying fn to a copy of the one
// published, then publishes and writes that copy. Changes made at once are
// applied one after another, each to what the one before published, so none
// is lost. It answers with what it published.
func (s *Store) UpdateSession(id string, fn func(*Session)) (*Session, error) {
	s.mu.Lock()
	cur := s.sessions[id]
	if s.deleted[id] || cur == nil {
		s.mu.Unlock()
		if cur == nil && !s.deleted[id] {
			return nil, fmt.Errorf("no session %s", id)
		}
		return nil, errSessionDeleted
	}
	c := *cur
	fn(&c)
	s.sessions[id] = &c
	s.mu.Unlock()
	return &c, s.flushMeta(id)
}

// flushMeta writes the session as it is now published. Writes are put in
// order, and each writes what is published when its turn comes, so the file
// ends up holding the newest whatever order the changes finish in.
func (s *Store) flushMeta(id string) error {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	s.mu.RLock()
	cur := s.sessions[id]
	s.mu.RUnlock()
	if cur == nil {
		return nil
	}
	return s.writeMeta(cur)
}

func (s *Store) writeMeta(sess *Session) error {
	// Held through the write, so a delete waits for it and then removes what
	// it wrote, rather than a late write recreating the directory after it.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.deleted[sess.ID] {
		return errSessionDeleted
	}
	if err := os.MkdirAll(s.sessionDir(sess.ID), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(sess, "", "  ")
	return os.WriteFile(filepath.Join(s.sessionDir(sess.ID), "meta.json"), b, 0o644)
}

// MarkRead moves where the operator has read a session through. It only moves
// forward: a read that arrives late, for a point already passed, is not a
// reason to call anything unread again. It reports whether the move cleared
// anything unread, which is the only change to a session a list would show.
func (s *Store) MarkRead(id string, through int) (bool, error) {
	s.mu.Lock()
	sess := s.sessions[id]
	if sess == nil || s.deleted[id] || through <= sess.ReadThrough {
		s.mu.Unlock()
		return false, nil
	}
	cleared := false
	for _, e := range liveEntries(s.entries[id]) {
		if e.Seq > sess.ReadThrough && e.Seq <= through && countsAsUnread(e) {
			cleared = true
			break
		}
	}
	c := *sess
	c.ReadThrough = through
	s.sessions[id] = &c
	s.mu.Unlock()
	return cleared, s.flushMeta(id)
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
	s.deleted[id] = true
	s.mu.Unlock()
	_, _ = s.db.Exec(`delete from entry_fts where session_id=?`, id)
	// After the mark, so a job written back by a wake still finishing is
	// either refused or already in the table to be deleted here.
	_, _ = s.db.Exec(`delete from jobs where session_id=?`, id)
	_, _ = s.db.Exec(`delete from job_runs where session_id=?`, id)
	return os.RemoveAll(s.sessionDir(id))
}

// Append writes an entry to the transcript. Entries are never modified once written.
func (s *Store) Append(sessionID string, e Entry) (Entry, error) {
	out, sess, err := s.appendEntries(sessionID, []Entry{e}, time.Now())
	if err != nil {
		if len(out) == 1 {
			return out[0], err
		}
		return e, err
	}
	s.index(sessionID, out[0])
	if sess != nil {
		_ = s.flushMeta(sessionID)
	}
	return out[0], nil
}

// AppendAll writes several entries to a transcript at once: one write to the
// file, one transaction for the search index, one write of the metadata. A fork
// copies a whole conversation, and copied an entry at a time it paid each of
// those once for every entry.
func (s *Store) AppendAll(sessionID string, in []Entry) ([]Entry, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out, sess, err := s.appendEntries(sessionID, in, time.Now())
	if err != nil {
		return out, err
	}
	if !s.wasDeleted(sessionID) {
		_ = s.indexAll(map[string][]Entry{sessionID: out})
	}
	if sess != nil {
		_ = s.flushMeta(sessionID)
	}
	return out, nil
}

// appendEntries numbers entries, holds them, and writes them, as one step. A
// write that fails takes them back out of memory, so what a conversation holds
// is what its file holds, and a number is never handed out twice. Before this,
// the host's disk filled during a write: the entry stayed in memory while half
// of it reached the file, the next entry was glued to that half, and after a
// restart both were gone and their numbers were given to new entries.
func (s *Store) appendEntries(sessionID string, in []Entry, now time.Time) ([]Entry, *Session, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.deleted[sessionID] {
		s.mu.Unlock()
		return nil, nil, errSessionDeleted
	}
	list := s.entries[sessionID]
	first := nextSeq(list)
	out := make([]Entry, len(in))
	for i, e := range in {
		e.Seq = first + i
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		if e.Type == "" {
			e.Type = "message"
		}
		out[i] = e
	}
	s.entries[sessionID] = append(list, out...)
	sess := s.sessions[sessionID]
	if sess != nil {
		c := *sess
		c.LastActiveAt = out[len(out)-1].CreatedAt
		s.sessions[sessionID] = &c
	}
	s.mu.Unlock()

	var buf bytes.Buffer
	for _, e := range out {
		line, _ := json.Marshal(e)
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := appendLines(filepath.Join(s.sessionDir(sessionID), "transcript.jsonl"), buf.Bytes()); err != nil {
		s.mu.Lock()
		// Writes are in order, so what this one added is still the end.
		if cur := s.entries[sessionID]; len(cur) >= len(out) && cur[len(cur)-1].Seq == out[len(out)-1].Seq {
			s.entries[sessionID] = cur[:len(cur)-len(out)]
		}
		if sess != nil && s.sessions[sessionID] != nil {
			c := *s.sessions[sessionID]
			c.LastActiveAt = sess.LastActiveAt
			s.sessions[sessionID] = &c
		}
		s.mu.Unlock()
		return out, nil, err
	}
	return out, sess, nil
}

// EntryBySeq is one entry of a conversation, found by its number.
func (s *Store) EntryBySeq(sessionID string, seq int) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.entries[sessionID]
	i := entryAt(list, seq)
	if i < 0 {
		return Entry{}, false
	}
	return list[i], true
}

// entryAt is the position of the entry numbered seq, or -1. A number is its
// position until a line cannot be read, so that is looked at first; a
// transcript that disagrees is searched rather than trusted.
func entryAt(list []Entry, seq int) int {
	if seq >= 0 && seq < len(list) && list[seq].Seq == seq {
		return seq
	}
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Seq == seq {
			return i
		}
	}
	return -1
}

// nextSeq is the number after the highest a transcript holds. It is not the
// count: a line that could not be read leaves a gap, and the count then hands
// out a number the file already has.
func nextSeq(list []Entry) int {
	n := 0
	for _, e := range list {
		if e.Seq >= n {
			n = e.Seq + 1
		}
	}
	return n
}

// appendLines adds whole lines to the end of a transcript. They start on a
// line of their own whatever the file ends with, so a line a crash or a full
// disk left unfinished stays one unreadable line rather than taking the next
// entry with it; and a write that fails is cut back off, so it leaves nothing.
func appendLines(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, size-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			b = append([]byte{'\n'}, b...)
		}
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Truncate(size)
		return err
	}
	return nil
}

func (s *Store) Entries(sessionID string) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, len(s.entries[sessionID]))
	copy(out, s.entries[sessionID])
	return out
}

type SearchHit struct {
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Seq       int       `json:"seq"`
	Snippet   string    `json:"snippet"`
	Type      string    `json:"type,omitempty"`
	Role      string    `json:"role,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	// Compacted says the session's newest compaction covers this entry, so the
	// agent no longer sees it — only the summary that stands for it.
	Compacted bool `json:"compacted"`
	// Reverted says a revert took this entry back, so the agent no longer sees
	// it at all.
	Reverted bool `json:"reverted"`
}

// Search queries the full-text index. A sessionID scopes it to one conversation,
// which is what lets a session reach back below its own compactions; empty
// searches every session.
func (s *Store) Search(q, sessionID string, limit int) ([]SearchHit, error) {
	out := []SearchHit{}
	match := matchWords(q)
	if match == "" {
		return out, nil
	}
	query := `select session_id, seq, snippet(entry_fts, 2, '[', ']', '…', 12) from entry_fts where entry_fts match ? limit ?`
	args := []any{match, limit}
	if sessionID != "" {
		query = `select session_id, seq, snippet(entry_fts, 2, '[', ']', '…', 12) from entry_fts where entry_fts match ? and session_id = ? limit ?`
		args = []any{match, sessionID, limit}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.SessionID, &h.Seq, &h.Snippet); err != nil {
			return nil, err
		}
		if sess := s.Session(h.SessionID); sess != nil {
			h.Title = sess.Title
		}
		s.describeHit(&h)
		out = append(out, h)
	}
	return out, nil
}

// matchWords turns the words a person or the agent is looking for into a
// query the index takes. Each word is quoted, so punctuation and words like
// AND are looked for rather than read as syntax — a hyphen is otherwise a
// column filter and a dot an error — and each matches as a prefix, so
// "repoint" finds "repointing". A word holding no letter or digit has nothing
// to match and is left out.
func matchWords(q string) string {
	var parts []string
	for _, w := range strings.Fields(q) {
		w = strings.ReplaceAll(w, `"`, "")
		if !strings.ContainsFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			continue
		}
		parts = append(parts, `"`+w+`"*`)
	}
	return strings.Join(parts, " ")
}

// describeHit fills in what the index does not hold: who wrote the entry, when,
// and whether a compaction or a revert has since taken it out of what the agent
// is sent.
func (s *Store) describeHit(h *SearchHit) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.entries[h.SessionID]
	gone := takenBack(list)
	h.Reverted = gone[h.Seq]
	// The compaction in force is the newest one a revert has not taken back.
	covers := 0
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Type == "compaction" && !gone[list[i].Seq] {
			covers = list[i].CoversThrough
			break
		}
	}
	h.Compacted = covers > 0 && h.Seq <= covers
	at := entryAt(list, h.Seq)
	if at >= 0 {
		e := list[at]
		h.Type, h.Role, h.CreatedAt = e.Type, e.Role, e.CreatedAt
	}
}

// ---- jobs ----

func (s *Store) PutJob(j *Job) error {
	// A wake still finishing when its session was deleted would otherwise put
	// the job back, to fail every tick against a session that is gone. The
	// lock is held through the write, so a delete cannot land between the two.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.deleted[j.SessionID] {
		return errSessionDeleted
	}
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

// JobCounts is how many jobs each session holds, in one query: the session list
// shows every session's count at once.
func (s *Store) JobCounts() (map[string]int, error) {
	rows, err := s.db.Query(`select session_id, count(*) from jobs group by session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
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

// ---- job runs ----

func (s *Store) PutJobRun(r JobRun) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.deleted[r.SessionID] {
		return errSessionDeleted
	}
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
	// An archive of a session deleted here is that session coming back on
	// purpose.
	s.mu.Lock()
	delete(s.deleted, sess.ID)
	s.mu.Unlock()
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
	// The index is derived: a write to it that fails costs searches, not the
	// conversation, and is not a reason to refuse the import.
	_, _ = s.db.Exec(`delete from entry_fts where session_id=?`, sess.ID)
	_ = s.indexAll(map[string][]Entry{sess.ID: entries})
	return nil
}
