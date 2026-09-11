package app

import (
	"encoding/json"
	"time"
)

// Entry is one line of a transcript. Entries are append-only and never rewritten.
type Entry struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"` // message | event
	Role       string          `json:"role,omitempty"`
	Text       string          `json:"text,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolResult json.RawMessage `json:"tool_result,omitempty"`
	Stderr     string          `json:"stderr,omitempty"`
	JobID      string          `json:"job_id,omitempty"`
	// DueAt is when a wake was scheduled for, which is not when it happened. A
	// host that sleeps takes the scheduler with it, and a reminder delivered two
	// hours late reads as current unless it says otherwise.
	DueAt       time.Time `json:"due_at,omitzero"`
	Status      string    `json:"status,omitempty"`     // fired | not_fired
	EventKind   string    `json:"event_kind,omitempty"` // job_check, job_error, error, fork, forked_from
	Usage       *Usage    `json:"usage,omitempty"`
	Sections    []Section `json:"sections,omitempty"`
	CarriedFrom string    `json:"carried_from,omitempty"`
	// CoversThrough is the last sequence number a compaction entry stands for.
	// The projection drops everything at or before it and sends this entry's
	// text instead, so a compaction is a rule rather than a rewrite: the covered
	// entries are still on disk, still searchable, and still readable.
	CoversThrough int `json:"covers_through,omitempty"`
	// FoldedTurns, TokensBefore, and TokensAfter are what the banner reports.
	// They are stored rather than recomputed so the figures shown cannot drift
	// if the estimator changes.
	FoldedTurns  int       `json:"folded_turns,omitempty"`
	TokensBefore int       `json:"tokens_before,omitempty"`
	TokensAfter  int       `json:"tokens_after,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// JobRun is one wake, kept on the job rather than in the session it fired into.
// A job outlives the conversation that created it, and the operator asks "has
// this been running?" at the job, not in a transcript.
type JobRun struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	SessionID string    `json:"session_id"`
	At        time.Time `json:"at"`
	DueAt     time.Time `json:"due_at,omitzero"`
	// Outcome is jobFired, jobSkipped, or jobFailed.
	Outcome string `json:"outcome"`
	// Message is what the wake produced: what the agent said, why the check said
	// no, or what went wrong. The transcript holds the whole exchange; this is
	// what makes the log readable without going there.
	Message string `json:"message"`
}

const (
	jobScheduled = "scheduled"
	jobDone      = "done"

	jobFired   = "fired"
	jobSkipped = "skipped"
	jobFailed  = "failed"
)

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Usage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	Cost             float64 `json:"cost"`
	TokensPerSecond  float64 `json:"tokens_per_second"`
}

type Section struct {
	Name     string `json:"name"`
	Text     string `json:"text"`
	Tokens   int    `json:"tokens"`
	Editable bool   `json:"editable"`
}

// SessionConfig is everything about a session that shapes its system prompt: the
// model that reads it, the tools defined in it, the skills indexed in it. It is
// chosen before the conversation starts and fixed from the session's first turn.
// Changing it after that means rotating into a successor that carries the
// conversation over.
type SessionConfig struct {
	Model         string   `json:"model"`
	EnabledTools  []string `json:"enabled_tools"`  // nil means every tool
	EnabledSkills []string `json:"enabled_skills"` // nil means every skill
}

// Session metadata. Stored as meta.json beside the transcript, so SQLite is derived.
// The config is embedded, so it flattens into the same JSON object it always was.
type Session struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	SessionConfig
	Status string `json:"status"` // active | archived
	Unread int    `json:"unread"`
	// ForkedFrom names the session this one was copied from. There is no
	// corresponding forward pointer: a fork does not supersede its origin, which
	// stays active and keeps its jobs, so no identifier ever comes to address
	// different content and nothing has to be followed forward.
	ForkedFrom string `json:"forked_from"`
	// CompactAtTokens and KeepVerbatimTokens are the whole trade between cost and
	// retention, and they are independent: the first is what a call costs at its
	// most expensive, the second is how much of the conversation is never lossy.
	CompactAtTokens    int        `json:"compact_at_tokens"`
	KeepVerbatimTokens int        `json:"keep_verbatim_tokens"`
	LastCompactedAt    *time.Time `json:"last_compacted_at"`
	Cost               float64    `json:"cost"`
	PromptTokens       int        `json:"prompt_tokens"`
	CachedTokens       int        `json:"cached_tokens"`
	CreatedAt          time.Time  `json:"created_at"`
	LastActiveAt       time.Time  `json:"last_active_at"`

	// Derived, not persisted.

	EntryCount   int     `json:"entry_count"`
	DiskBytes    int64   `json:"disk_bytes"` // transcript plus working directory
	ContextUsed  int     `json:"context_used"`
	JobCount     int     `json:"job_count"`
	CacheHitRate float64 `json:"cache_hit_rate"`
}

func (s *Session) toolEnabled(name string) bool  { return inSet(s.EnabledTools, name) }
func (s *Session) skillEnabled(name string) bool { return inSet(s.EnabledSkills, name) }

// sameAs reports whether two configurations would produce the same prompt.
func (c SessionConfig) sameAs(o SessionConfig) bool {
	return c.Model == o.Model && sameSet(c.EnabledTools, o.EnabledTools) &&
		sameSet(c.EnabledSkills, o.EnabledSkills)
}

func sameSet(a, b []string) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func inSet(set []string, name string) bool {
	if set == nil {
		return true
	}
	for _, v := range set {
		if v == name {
			return true
		}
	}
	return false
}

type Job struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`

	// The three settings that decide everything. A job has no kind: a check gates
	// whether a wake acts, and after_acting says whether the job survives a wake
	// that did.
	Schedule string `json:"schedule"`
	Check    string `json:"check"`
	Prompt   string `json:"prompt"`
	// AfterActing is afterStop or afterContinue. It is named for the moment it
	// applies to rather than for the schedule: called "repeat", models read it as
	// a question about cadence and answered it from the request's own "check
	// every two minutes", ending a finished watch that should have stopped.
	AfterActing string `json:"after_acting"`

	// Status is jobScheduled until the job has nothing left to do, then jobDone.
	// A finished job is kept rather than deleted: its log is the record of what
	// it did, and deleting the row would delete the answer along with it.
	Status string `json:"status"`
	// Estimate is computed on the way out, never stored: what this job will send
	// and what that costs, so a cadence is chosen with the price in view.
	Estimate   *JobEstimate `json:"estimate,omitempty"`
	RunCount   int          `json:"run_count"`
	FailCount  int          `json:"fail_count"`
	NextRunAt  time.Time    `json:"next_run_at"`
	LastStatus string       `json:"last_status"`
	CreatedAt  time.Time    `json:"created_at"`
}

const (
	afterStop     = "stop"
	afterContinue = "continue"
)

type MemoryItem struct {
	ID            string    `json:"id"`
	Text          string    `json:"text"`
	SourceSession string    `json:"source_session"`
	CreatedAt     time.Time `json:"created_at"`
}

func estTokens(s string) int { return (len(s) + 3) / 4 }
