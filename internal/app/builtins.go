package app

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A builtin is a tool that runs inside the agent. Each one is the model's
// handle on something that is the agent's own and cannot be handed to a
// program in the sandbox: the jobs, the transcripts, the skills, the model
// key, the package store, the operator (ADR-086). reload_tools is the registry's own
// control surface.
//
// What the model is sent for a builtin is a manifest.json like any tool's —
// name, description, parameters — and its eval cases sit beside it. They are
// compiled in: a builtin is part of the agent and changes only with it.

//go:embed builtin
var builtinFiles embed.FS

// builtinRuns is what each builtin does, by the name its manifest gives it.
func (a *App) builtinRuns() map[string]builtinFn {
	return map[string]builtinFn{
		"schedule":       a.scheduleTool,
		"session_search": a.searchTool,
		"skill_read":     a.skillReadTool,
		"call_model":     a.callModelTool,
		"packages":       a.packagesTool,
		"ask_operator":   a.askOperatorTool,
	}
}

// builtinNames are the builtins that carry a manifest, in order.
func builtinNames() []string {
	entries, _ := fs.ReadDir(builtinFiles, "builtin")
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func (a *App) registerBuiltins() {
	runs := a.builtinRuns()
	a.tools.mu.Lock()
	defer a.tools.mu.Unlock()
	a.tools.register(&Tool{
		Name:        "reload_tools",
		Description: "Validate and register tools from disk.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}},
		Builtin:     true,
		run:         a.reloadTool,
	})
	for _, name := range builtinNames() {
		raw, err := builtinFiles.ReadFile(path.Join("builtin", name, "manifest.json"))
		var m manifest
		if err == nil {
			err = json.Unmarshal(raw, &m)
		}
		run := runs[m.Name]
		if err != nil || m.Name != name || run == nil {
			// Compiled in, so this is a mistake in the agent and not in what
			// it was given; a test holds every manifest to its builtin.
			panic(fmt.Sprintf("builtin %s: manifest and code disagree (%v)", name, err))
		}
		a.tools.register(&Tool{Name: m.Name, Description: m.Description, Parameters: m.Parameters,
			Builtin: true, run: run})
	}
}

func (a *App) reloadTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	loaded, failures := a.ReloadTools(tc.SessionID)
	var sb strings.Builder
	sb.WriteString("loaded: " + strings.Join(loaded, ", ") + "\n")
	for _, f := range failures {
		sb.WriteString("failed " + f.Dir + ": " + f.Reason + "\n")
	}
	return sb.String(), nil
}

// toolArgs decodes a call's arguments. Arguments that are not the object the
// schema describes are the model's mistake, said in words it can act on.
func toolArgs(args json.RawMessage, v any) error {
	if len(strings.TrimSpace(string(args))) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("the arguments are not what this tool takes: %v", err)
	}
	return nil
}

// ---- schedule ----

// scheduleTool creates, lists, edits, and deletes the calling conversation's
// jobs — the rows that make future work visible before it happens.
func (a *App) scheduleTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Action      string `json:"action"`
		ID          string `json:"id"`
		SessionID   string `json:"session_id"`
		Schedule    string `json:"schedule"`
		Check       string `json:"check"`
		Prompt      string `json:"prompt"`
		AfterActing string `json:"after_acting"`
	}
	if err := toolArgs(args, &in); err != nil {
		return nil, err
	}
	// Another conversation's job is not there, as far as this one can tell.
	own := func(id string) (*Job, error) {
		j, err := a.store.Job(id)
		if err != nil || j == nil || j.SessionID != tc.SessionID {
			return nil, fmt.Errorf("no such job")
		}
		return j, nil
	}
	switch in.Action {
	case "create":
		// Naming another conversation is refused rather than corrected: a job
		// wakes its conversation with a prompt, and one that asked for someone
		// else's should be told it did not get it.
		if in.SessionID != "" && in.SessionID != tc.SessionID {
			return nil, fmt.Errorf("a tool schedules into its own conversation only, not %s", in.SessionID)
		}
		j, err := a.CreateJob(JobSpec{SessionID: tc.SessionID, Schedule: in.Schedule, Check: in.Check,
			Prompt: in.Prompt, AfterActing: in.AfterActing})
		if err != nil {
			return nil, err
		}
		j = a.priceJobs(ctx, []*Job{j})[0]
		return fmt.Sprintf("job %s created · wakes %s · %s after acting%s",
			j.ID, j.NextRunAt.Format(time.RFC3339), j.AfterActing, jobCost(j)), nil
	case "delete":
		id := in.ID
		if id == "" {
			id = tc.JobID
		}
		if id == "" {
			return nil, fmt.Errorf("id is required")
		}
		if _, err := own(id); err != nil {
			return nil, err
		}
		if err := a.store.DeleteJob(id); err != nil {
			return nil, err
		}
		a.hub.Broadcast(wsEvent{Kind: "jobs"})
		return fmt.Sprintf("job %s deleted", id), nil
	case "edit":
		if in.ID == "" {
			return nil, fmt.Errorf("id is required")
		}
		j, err := own(in.ID)
		if err != nil {
			return nil, err
		}
		given := func(s string) *string {
			if s == "" {
				return nil
			}
			return &s
		}
		if err := a.editJob(j, jobEdit{Schedule: given(in.Schedule), Check: given(in.Check),
			Prompt: given(in.Prompt), AfterActing: given(in.AfterActing)}); err != nil {
			return nil, err
		}
		return fmt.Sprintf("job %s updated", in.ID), nil
	}
	jobs, err := a.store.SessionJobs(tc.SessionID)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return "no jobs in this session", nil
	}
	lines := make([]string, 0, len(jobs))
	for _, j := range a.priceJobs(ctx, jobs) {
		lines = append(lines, jobLine(j))
	}
	return strings.Join(lines, "\n"), nil
}

// jobLine is one job as the agent reads it in a list.
func jobLine(j *Job) string {
	status := j.Status
	if status == "" {
		status = jobScheduled
	}
	check := j.Check
	if check == "" {
		check = "(none)"
	}
	// A job that has finished has no next wake, and printing the stale one
	// reads as though it will fire again.
	next := j.NextRunAt.Format(time.RFC3339)
	if status == jobDone {
		next = "(none)"
	}
	return fmt.Sprintf("%s · %s · schedule %s · check %s · %s after acting · %d runs · next wake %s%s · %s",
		j.ID, status, j.Schedule, check, j.AfterActing, j.RunCount, next, jobCost(j), truncate(j.Prompt, 100))
}

// jobCost is what a job will send, and what that comes to. Nothing refuses a
// cadence any more (ADR-029), so the price is what is decided on and it has to
// be visible at the moment one is chosen. A job with no estimate says nothing
// about cost, which is different from one that costs nothing.
func jobCost(j *Job) string {
	e := j.Estimate
	if e == nil {
		return ""
	}
	if e.Gated {
		return " · gated by its check: no model cost until it passes, then ~" +
			commas(e.TokensPerTurn) + " tok a turn"
	}
	if e.WakesPerDay == 0 {
		return " · one wake, ~" + commas(e.TokensPerTurn) + " tok"
	}
	out := fmt.Sprintf(" · ~%.1f wakes a day, ~%s tok a day", e.WakesPerDay, commas(e.TokensPerDay))
	if e.Priced {
		out += fmt.Sprintf(" (~$%.2f a day, ~$%.2f a month)", e.CostPerDay, e.CostPerMonth)
	}
	return out
}

// commas groups a token count for reading. A number whose digits have to be
// counted is a number that will not be read.
func commas(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// ---- session_search ----

// searchReadLimit is the most of one entry a read returns. A tool result can
// be megabytes, and the model is sent all of what it is given.
const searchReadLimit = 20000

// searchTool finds entries by their words, or reads one entry whole. A search
// hit is a dozen words around the match, and an agent that lost an answer to a
// compaction once spent four hours and 696 searches putting it back together
// from those. The calling conversation unless another is named.
func (a *App) searchTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Query   string `json:"query"`
		Seq     *int   `json:"seq"`
		Session string `json:"session"`
		Limit   int    `json:"limit"`
	}
	if err := toolArgs(args, &in); err != nil {
		return nil, err
	}
	scope := strings.TrimSpace(in.Session)
	if in.Seq != nil {
		return a.readEntry(tc, *in.Seq, scope)
	}
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, fmt.Errorf("query is required, or seq to read one entry whole")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if scope == "" {
		scope = tc.SessionID
	}
	if scope == "all" {
		scope = ""
	}
	hits, err := a.store.Search(q, scope, limit)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		if scope != "" {
			return fmt.Sprintf("no matches in this session for %q. Pass session:\"all\" to search every conversation.", q), nil
		}
		return fmt.Sprintf("no matches for %q in any session", q), nil
	}
	lines := make([]string, 0, len(hits))
	for _, h := range hits {
		lines = append(lines, fmt.Sprintf("%s #%d (%s): %s", h.SessionID, h.Seq, h.Title, h.Snippet))
	}
	return strings.Join(lines, "\n"), nil
}

func (a *App) readEntry(tc *ToolCtx, seq int, scope string) (any, error) {
	if scope == "all" {
		return nil, fmt.Errorf("a number names an entry in one conversation; pass its session id, or omit session for this one")
	}
	if scope == "" {
		scope = tc.SessionID
	}
	if a.store.Session(scope) == nil {
		return nil, fmt.Errorf("no conversation %s", scope)
	}
	e, ok := a.store.EntryBySeq(scope, seq)
	if !ok {
		return nil, fmt.Errorf("no entry %d in %s", seq, scope)
	}
	h := SearchHit{SessionID: scope, Seq: seq}
	a.store.describeHit(&h)
	who := e.Role
	if who == "" {
		who = e.Type
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s, %s", e.Seq, who, e.CreatedAt.Format(time.RFC3339))
	if h.Compacted {
		b.WriteString(" (folded away by a compaction; you no longer see it otherwise)")
	}
	if h.Reverted {
		b.WriteString(" (taken back by a revert)")
	}
	b.WriteString("\n\n")
	if e.Text != "" {
		b.WriteString(e.Text)
		b.WriteString("\n")
	}
	for _, c := range e.ToolCalls {
		fmt.Fprintf(&b, "called %s %s\n", c.Name, c.Arguments)
	}
	if len(e.ToolResult) > 0 {
		var r toolResult
		_ = json.Unmarshal(e.ToolResult, &r)
		fmt.Fprintf(&b, "%s returned: %s%s\n", e.ToolName, r.Content, r.Error)
	}
	out := b.String()
	if len(out) > searchReadLimit {
		out = truncate(out, searchReadLimit) + fmt.Sprintf("\n[cut at %d characters]", searchReadLimit)
	}
	return out, nil
}

// ---- skill_read ----

// skillReadTool reads a skill the calling conversation has, and no other: its
// file as stored, front matter and all, so a skill is an example of the format
// and a revision starts from the whole of it; or one of the files beside it.
func (a *App) skillReadTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Name string `json:"name"`
		File string `json:"file"`
	}
	if err := toolArgs(args, &in); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	sess := a.store.Session(tc.SessionID)
	sk := a.skills.Get(name)
	if sess == nil || sk == nil || !a.hasSkill(sess, sk) {
		return nil, fmt.Errorf("no such skill in this conversation")
	}
	if file := strings.Trim(strings.TrimSpace(in.File), "/"); file != "" {
		var have []string
		for _, f := range sk.Files {
			if f.Path == file {
				return f.Body, nil
			}
			have = append(have, f.Path)
		}
		if len(have) == 0 {
			return nil, fmt.Errorf("the skill %s has no file %q; it is one file, and skill_read without a file reads it", sk.Name, file)
		}
		return nil, fmt.Errorf("the skill %s has no file %q; its files are %s", sk.Name, file, strings.Join(have, ", "))
	}
	text := sk.Source
	if text == "" {
		text = sk.Body
	}
	if len(sk.Files) == 0 {
		return text, nil
	}
	var sb strings.Builder
	sb.WriteString(strings.TrimRight(text, "\n"))
	sb.WriteString("\n\n---\n")
	// A folder says where it is: its scripts run from there (ADR-071).
	if sk.Main() == skillMain {
		dir := absOr(filepath.Dir(sk.Path))
		fmt.Fprintf(&sb, "This skill's files are in %s, readable and runnable from there with bash "+
			"(e.g. python3 %s/%s) and not writable. ", dir, dir, sk.Files[0].Path)
	}
	fmt.Fprintf(&sb, "Read one with skill_read, its path as \"file\" — "+
		"for example {\"name\": %q, \"file\": %q}:\n", name, sk.Files[0].Path)
	for _, f := range sk.Files {
		fmt.Fprintf(&sb, "- %s (%d bytes)\n", f.Path, f.Bytes)
	}
	return sb.String(), nil
}

// ---- call_model ----

// callModelTool puts a request to another model on the conversation's behalf.
// It is here and not in a program because the call carries the model key,
// which no program is handed.
func (a *App) callModelTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var c modelCall
	if err := toolArgs(args, &c); err != nil {
		return nil, err
	}
	if c.Model == "" && c.List == "" {
		return nil, fmt.Errorf("name a model, or set list to text, speech, transcription, or decisions to find one")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	content, cost, err := a.callModel(ctx, tc.SessionID, c)
	if err != nil {
		return nil, err
	}
	if cost > 0 {
		_, _ = a.store.UpdateSession(tc.SessionID, func(x *Session) { x.Cost += cost })
		a.hub.Broadcast(wsEvent{Kind: "sessions"})
	}
	return content, nil
}

// ---- packages ----

// packagesTool searches for, installs, and lists the programs a conversation
// has from Nix (ADR-072). The store is the agent's: no program in the sandbox
// can write it.
func (a *App) packagesTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Search  string   `json:"search"`
		Install []string `json:"install"`
	}
	if err := toolArgs(args, &in); err != nil {
		return nil, err
	}
	if in.Search != "" && len(in.Install) > 0 {
		return nil, fmt.Errorf("search or install, not both: search first, then install a name the search listed")
	}
	if a.pkgs == nil {
		return nil, fmt.Errorf("packages cannot be installed in this agent")
	}
	s := a.store.Session(tc.SessionID)
	if s == nil {
		return nil, fmt.Errorf("no such session")
	}
	switch {
	case in.Search != "":
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		return a.pkgs.Search(ctx, in.Search)
	case len(in.Install) > 0:
		ctx, cancel := context.WithTimeout(ctx, 25*time.Minute)
		defer cancel()
		content, err := a.pkgs.Install(ctx, s, in.Install, func(pk Package) error {
			_, err := a.store.UpdateSession(s.ID, func(x *Session) {
				for i, have := range x.Packages {
					if have.Name == pk.Name {
						x.Packages[i] = pk
						return
					}
				}
				x.Packages = append(x.Packages, pk)
			})
			return err
		})
		if err != nil {
			return nil, err
		}
		a.hub.Broadcast(wsEvent{Kind: "sessions"})
		return content, nil
	}
	return listPackages(s.Packages), nil
}
