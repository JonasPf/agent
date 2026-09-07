package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// An eval asks a real model to use a tool and checks what it did. It is the only
// thing that tests a tool's description, which is the surface the model actually
// programmes against — a schema the system handles perfectly can still be one no
// model reads correctly, and nothing deterministic can see that.
//
// Every tool directory carries an eval.json. A tool without one is a tool whose
// description has never been read by a model on purpose.

const evalFile = "eval.json"

// EvalCase is one request put to the model, and what its tool use must look like.
type EvalCase struct {
	Name   string   `json:"name"`
	Prompt string   `json:"prompt"`
	Tools  []string `json:"tools,omitempty"` // enabled for this case; nil means all
	// Files are written into the case's working directory before the turn. A
	// tool that operates on files cannot be evaluated without them: a prompt
	// that says "the file already exists" against an empty directory tests the
	// model's willingness to hallucinate, not the tool's description.
	Files  map[string]string `json:"files,omitempty"`
	Expect Expect            `json:"expect"`
}

// Expect describes the tool use a case requires. Every field is optional; an
// empty Expect asserts only that the turn completed.
type Expect struct {
	Tool      string            `json:"tool,omitempty"`       // must be called at least once
	Args      map[string]any    `json:"args,omitempty"`       // exact values on that call
	ArgsMatch map[string]string `json:"args_match,omitempty"` // regexps on that call
	NotTools  []string          `json:"not_tools,omitempty"`  // must not be called
	// Clean requires that the tool under test never returned an error — that the
	// model got its shape right first time, rather than learning it from a
	// rejection. It is scoped to that tool on purpose: a probe with bash that
	// exits non-zero is a legitimate answer to a question, not a failure, and
	// counting it would punish the agent for testing a check before using it.
	// With no Tool set, Clean applies to every call in the turn.
	Clean bool `json:"clean"`
}

type EvalSuite struct {
	Tool  string     `json:"-"`
	Cases []EvalCase `json:"cases"`
}

// LoadEvalSuites reads every tool directory's eval.json, in tool-name order.
func LoadEvalSuites(toolsDir string) ([]EvalSuite, error) {
	entries, err := os.ReadDir(toolsDir)
	if err != nil {
		return nil, err
	}
	var out []EvalSuite
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := loadEvalSuite(filepath.Join(toolsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out, nil
}

func loadEvalSuite(dir string) (EvalSuite, error) {
	name := filepath.Base(dir)
	b, err := os.ReadFile(filepath.Join(dir, evalFile))
	if err != nil {
		return EvalSuite{Tool: name}, fmt.Errorf("%s: %w", evalFile, err)
	}
	var s EvalSuite
	if err := json.Unmarshal(b, &s); err != nil {
		return EvalSuite{Tool: name}, fmt.Errorf("%s: %w", evalFile, err)
	}
	s.Tool = name
	if len(s.Cases) == 0 {
		return s, fmt.Errorf("%s defines no cases", evalFile)
	}
	for i, c := range s.Cases {
		if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Prompt) == "" {
			return s, fmt.Errorf("case %d needs a name and a prompt", i)
		}
	}
	return s, nil
}

// TurnRecord is what one eval case actually did, read back from the transcript.
type TurnRecord struct {
	Calls  []RecordedCall
	Errors []string // tool results that came back not-ok
	Text   string   // the assistant's last words
}

// errorsFrom returns the failed results of one tool, or of every tool when the
// name is empty.
func (r TurnRecord) errorsFrom(tool string) []string {
	if tool == "" {
		return r.Errors
	}
	var out []string
	for _, e := range r.Errors {
		if strings.HasPrefix(e, tool+": ") {
			out = append(out, e)
		}
	}
	return out
}

type RecordedCall struct {
	Name string
	Args map[string]any
}

// Check reports why a record fails an expectation, or nil if it satisfies it.
func (e Expect) Check(r TurnRecord) []string {
	var bad []string
	if e.Clean {
		if errs := r.errorsFrom(e.Tool); len(errs) > 0 {
			bad = append(bad, "rejected before it got the shape right: "+strings.Join(errs, "; "))
		}
	}
	for _, n := range e.NotTools {
		for _, c := range r.Calls {
			if c.Name == n {
				bad = append(bad, "called "+n+", which this case forbids")
				break
			}
		}
	}
	if e.Tool == "" {
		return bad
	}
	var matches []RecordedCall
	for _, c := range r.Calls {
		if c.Name == e.Tool {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		bad = append(bad, fmt.Sprintf("never called %s (called: %s)", e.Tool, called(r)))
		return bad
	}
	// Any one call satisfying every constraint is enough; report the closest.
	var best []string
	for _, c := range matches {
		if problems := e.checkArgs(c); len(problems) == 0 {
			return bad
		} else if best == nil || len(problems) < len(best) {
			best = problems
		}
	}
	return append(bad, best...)
}

func (e Expect) checkArgs(c RecordedCall) []string {
	var bad []string
	for k, want := range e.Args {
		got, ok := c.Args[k]
		if !ok {
			bad = append(bad, fmt.Sprintf("%s.%s missing, want %v", c.Name, k, want))
			continue
		}
		if !sameJSON(got, want) {
			bad = append(bad, fmt.Sprintf("%s.%s = %v, want %v", c.Name, k, got, want))
		}
	}
	for k, pattern := range e.ArgsMatch {
		got, ok := c.Args[k]
		if !ok {
			bad = append(bad, fmt.Sprintf("%s.%s missing, want match %s", c.Name, k, pattern))
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s.%s: bad pattern %s: %v", c.Name, k, pattern, err))
			continue
		}
		if !re.MatchString(fmt.Sprint(got)) {
			bad = append(bad, fmt.Sprintf("%s.%s = %v, want match %s", c.Name, k, got, pattern))
		}
	}
	return bad
}

// sameJSON compares two decoded JSON values, tolerating the number and boolean
// shapes a model may emit for the same value.
func sameJSON(got, want any) bool {
	if fmt.Sprint(got) == fmt.Sprint(want) {
		return true
	}
	gb, err1 := json.Marshal(got)
	wb, err2 := json.Marshal(want)
	return err1 == nil && err2 == nil && string(gb) == string(wb)
}

func called(r TurnRecord) string {
	if len(r.Calls) == 0 {
		return "nothing"
	}
	var names []string
	for _, c := range r.Calls {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

// EvalResult is one case's outcome.
type EvalResult struct {
	Tool     string
	Case     string
	Failures []string
	Err      error
	Record   TurnRecord
	Took     time.Duration
}

func (r EvalResult) Passed() bool { return r.Err == nil && len(r.Failures) == 0 }

// RunEvalCase puts one case to the model in a throwaway session and reads back
// what happened. The session, and anything the case scheduled, is removed after.
func (a *App) RunEvalCase(ctx context.Context, model string, c EvalCase) (res EvalResult) {
	res = EvalResult{Case: c.Name}
	start := time.Now()
	defer func() { res.Took = time.Since(start) }()

	cfg := SessionConfig{Model: model, EnabledTools: c.Tools}
	s, err := a.NewSession(cfg, "")
	if err != nil {
		res.Err = err
		return res
	}
	defer func() { _ = a.store.DeleteSession(s.ID) }()

	if err := seedFiles(a.ensureWorkspace(s.ID), c.Files); err != nil {
		res.Err = err
		return res
	}

	if err := a.runTurn(ctx, s, turnOpts{UserText: c.Prompt}); err != nil {
		res.Err = err
		return res
	}
	res.Record = readTurn(a.store.Entries(s.ID))
	res.Failures = c.Expect.Check(res.Record)
	return res
}

// seedFiles writes a case's fixtures into its working directory. A path that
// climbs out of it is a broken case rather than a security question — the tool
// is confined regardless — but it is still refused, because a case that writes
// somewhere else is not testing what it says it is.
func seedFiles(workspace string, files map[string]string) error {
	for name, body := range files {
		path, err := filepath.Abs(filepath.Join(workspace, name))
		if err != nil {
			return err
		}
		root, err := filepath.Abs(workspace)
		if err != nil {
			return err
		}
		if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
			return fmt.Errorf("eval file %q is outside the case's working directory", name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readTurn(entries []Entry) TurnRecord {
	var r TurnRecord
	for _, e := range entries {
		switch {
		case e.Role == "assistant":
			if e.Text != "" {
				r.Text = e.Text
			}
			for _, c := range e.ToolCalls {
				args := map[string]any{}
				_ = json.Unmarshal([]byte(c.Arguments), &args)
				r.Calls = append(r.Calls, RecordedCall{Name: c.Name, Args: args})
			}
		case e.Role == "tool":
			var out toolResult
			if json.Unmarshal(e.ToolResult, &out) == nil && !out.OK {
				r.Errors = append(r.Errors, e.ToolName+": "+truncate(out.Error, 120))
			}
		}
	}
	return r
}

const defaultEvalModel = "minimax/minimax-m3:free"

// EvalModel is the model the eval command drives. It is deliberately a cheap one
// by default: an eval that is expensive to run is an eval nobody runs.
func EvalModel() string { return envOr("AGENT_EVAL_MODEL", defaultEvalModel) }

// RunEvals runs the eval suites for the named tools, or all of them, and writes
// a report. It returns an error when any case fails, so it can gate a change.
func RunEvals(only []string, w io.Writer) error {
	cfg := LoadConfig()
	if cfg.APIKey == "" {
		return fmt.Errorf("OPENROUTER_API_KEY is not set; evals drive a real model")
	}
	suites, err := LoadEvalSuites(cfg.ToolsDir)
	if err != nil {
		return err
	}
	if len(only) > 0 {
		suites = filterSuites(suites, only)
		if len(suites) == 0 {
			return fmt.Errorf("no eval suites match %v", only)
		}
	}

	// A temporary data directory, the real tools directory, and a real model.
	// Nothing about the system is substituted except where the work lands.
	dir, err := os.MkdirTemp("", "agent-eval-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	// Tools reach the system over AGENT_URL, so the eval serves its own API on
	// an ephemeral port. Without this every tool call would land on whatever
	// happens to be listening on the default port, against a different store.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()
	cfg.Addr = ln.Addr().String()
	cfg.DataDir = dir
	cfg.Workspace = filepath.Join(dir, "workspace")
	if err := os.MkdirAll(cfg.Workspace, 0o755); err != nil {
		return err
	}
	st, err := OpenStore(cfg.DataDir)
	if err != nil {
		return err
	}
	dbPath := filepath.Join(cfg.DataDir, "agent.db")
	sandbox := NewSandbox(cfg)
	a := &App{cfg: cfg, sandbox: sandbox, store: st,
		tools:  NewRegistry(cfg.ToolsDir, dbPath, st.DB()),
		skills: NewSkills(cfg.SkillsDir),
		or:     NewOpenRouter(cfg.APIKey),
		hub:    NewHub(),
		queues: map[string]chan func(){}, busy: map[string]bool{}}
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		return fmt.Errorf("tools failed to load: %v", failures)
	}
	a.sched = NewScheduler(a)
	go func() { _ = http.Serve(ln, a.routes()) }()

	model := EvalModel()
	fmt.Fprintf(w, "model %s\n\n", model)
	var run, failed int
	for _, s := range suites {
		fmt.Fprintf(w, "%s\n", s.Tool)
		for _, c := range s.Cases {
			res := a.runEvalCaseWithRetry(context.Background(), model, c, w)
			res.Tool = s.Tool
			run++
			if res.Passed() {
				fmt.Fprintf(w, "  PASS  %-44s %s → %s\n", c.Name, res.Took.Round(time.Millisecond), called(res.Record))
				continue
			}
			failed++
			fmt.Fprintf(w, "  FAIL  %-44s %s\n", c.Name, res.Took.Round(time.Millisecond))
			fmt.Fprintf(w, "        asked: %s\n", c.Prompt)
			if res.Err != nil {
				fmt.Fprintf(w, "        error: %v\n", res.Err)
			}
			for _, f := range res.Failures {
				fmt.Fprintf(w, "        %s\n", f)
			}
			for _, call := range res.Record.Calls {
				b, _ := json.Marshal(call.Args)
				fmt.Fprintf(w, "        saw:   %s %s\n", call.Name, truncate(string(b), 160))
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "%d cases, %d failed\n", run, failed)
	if failed > 0 {
		return fmt.Errorf("%d of %d eval cases failed", failed, run)
	}
	return nil
}

// runEvalCaseWithRetry retries a case that failed for a reason that is not about
// the model's choices. A provider queue is not an eval result.
func (a *App) runEvalCaseWithRetry(ctx context.Context, model string, c EvalCase, w io.Writer) EvalResult {
	var res EvalResult
	for attempt := 1; attempt <= 3; attempt++ {
		res = a.RunEvalCase(ctx, model, c)
		if res.Err == nil || !transient(res.Err) {
			return res
		}
		wait := time.Duration(attempt*20) * time.Second
		fmt.Fprintf(w, "  ....  %-44s rate limited, retrying in %s\n", c.Name, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return res
		}
	}
	return res
}

func transient(err error) bool {
	s := err.Error()
	return strings.Contains(s, "429") || strings.Contains(s, "rate limit") ||
		strings.Contains(s, "rate-limited") || strings.Contains(s, "503")
}

func filterSuites(all []EvalSuite, only []string) []EvalSuite {
	want := map[string]bool{}
	for _, n := range only {
		want[n] = true
	}
	var out []EvalSuite
	for _, s := range all {
		if want[s.Tool] {
			out = append(out, s)
		}
	}
	return out
}
