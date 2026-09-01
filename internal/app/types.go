package app

import (
	"encoding/json"
	"time"
)

// Entry is one line of a transcript. Entries are append-only and never rewritten.
type Entry struct {
	Seq         int             `json:"seq"`
	Type        string          `json:"type"` // message | event
	Role        string          `json:"role,omitempty"`
	Text        string          `json:"text,omitempty"`
	ToolCalls   []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	ToolName    string          `json:"tool_name,omitempty"`
	ToolResult  json.RawMessage `json:"tool_result,omitempty"`
	Stderr      string          `json:"stderr,omitempty"`
	JobID       string          `json:"job_id,omitempty"`
	Status      string          `json:"status,omitempty"`     // fired | not_fired
	EventKind   string          `json:"event_kind,omitempty"` // prompt, job_check, summary, ...
	Usage       *Usage          `json:"usage,omitempty"`
	Sections    []Section       `json:"sections,omitempty"`
	CarriedFrom string          `json:"carried_from,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

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

// Session metadata. Stored as meta.json beside the transcript, so SQLite is derived.
type Session struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Model           string     `json:"model"`
	Status          string     `json:"status"` // active | archived
	EnabledTools    []string   `json:"enabled_tools"`
	EnabledSkills   []string   `json:"enabled_skills"`
	Muted           bool       `json:"muted"`
	Unread          int        `json:"unread"`
	Summary         string     `json:"summary"`
	SummaryUpdated  *time.Time `json:"summary_updated_at"`
	SummarySeq      int        `json:"summary_seq"`
	ContinuedFrom   string     `json:"continued_from"`
	ContinuedBy     string     `json:"continued_by"`
	RotateAtTokens  int        `json:"rotate_at_tokens"`
	CarryOverTokens int        `json:"carry_over_tokens"`
	Cost            float64    `json:"cost"`
	PromptTokens    int        `json:"prompt_tokens"`
	CachedTokens    int        `json:"cached_tokens"`
	CreatedAt       time.Time  `json:"created_at"`
	LastActiveAt    time.Time  `json:"last_active_at"`

	// Derived, not persisted.
	EntryCount   int     `json:"entry_count"`
	ContextUsed  int     `json:"context_used"`
	JobCount     int     `json:"job_count"`
	CacheHitRate float64 `json:"cache_hit_rate"`
}

func (s *Session) toolEnabled(name string) bool  { return inSet(s.EnabledTools, name) }
func (s *Session) skillEnabled(name string) bool { return inSet(s.EnabledSkills, name) }

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
	ID             string    `json:"id"`
	SessionID      string    `json:"session_id"`
	Schedule       string    `json:"schedule"`
	Check          string    `json:"check"`
	Prompt         string    `json:"prompt"`
	ExpiresAt      time.Time `json:"expires_at"`
	OnConditionMet string    `json:"on_condition_met"` // delete | continue
	RunCount       int       `json:"run_count"`
	FailCount      int       `json:"fail_count"`
	NextRunAt      time.Time `json:"next_run_at"`
	LastStatus     string    `json:"last_status"`
	CreatedAt      time.Time `json:"created_at"`

	// Derived from check and schedule, not persisted as a column.
	Kind string `json:"kind"` // check | due | judgement
}

type DeadLetter struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	SessionID string    `json:"session_id"`
	JobSpec   Job       `json:"job_spec"`
	Reason    string    `json:"reason"` // expired | error | unavailable
	Detail    string    `json:"detail"`
	RunCount  int       `json:"run_count"`
	Status    string    `json:"status"` // open | closed
	CreatedAt time.Time `json:"created_at"`
}

type MemoryItem struct {
	ID            string    `json:"id"`
	Text          string    `json:"text"`
	SourceSession string    `json:"source_session"`
	CreatedAt     time.Time `json:"created_at"`
}

func estTokens(s string) int { return (len(s) + 3) / 4 }
