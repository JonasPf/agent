package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tool is either a builtin or a directory containing a manifest and an executable.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	// Agent is the manifest's "agent" block. A builtin has no manifest and
	// leaves it empty.
	Agent    ToolAgent
	HasPanel bool
	LoadedAt time.Time
	Builtin  bool
	Dir      string
	run      builtinFn
}

// ToolAgent is what a manifest says under "agent": everything this agent reads
// to run the tool, and never sends to the model.
type ToolAgent struct {
	DBPrefix string `json:"db_prefix"`
	Timeout  int    `json:"timeout_seconds"`
	// Reads names the paths this tool may read beyond the runtime every tool
	// gets. A browser reads /proc and /sys before it renders anything; a tool
	// that edits a file does not, and granting the union of what any tool might
	// need would hand every tool the widest boundary any of them asks for.
	Reads []string `json:"reads,omitempty"`
	// Env names variables from the agent's own environment that this tool is
	// given, because the service it talks to needs a credential and there is
	// nowhere else for one to come from. It is declared per tool for the reason
	// Reads is: web_search needs a search key, and bash — which runs commands
	// the model wrote — must never be handed one. The value is never shown
	// anywhere; the name is, on the tool's own card.
	Env []string `json:"env,omitempty"`
}

// manifest is manifest.json. Its top level is exactly an OpenAI function
// definition, which is what the model is sent; the agent's own settings sit
// under "agent", so that neither can be mistaken for the other (ADR-067).
type manifest struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Agent       ToolAgent      `json:"agent"`
}

type builtinFn func(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error)

// ToolCtx is what a tool call knows about where it is running.
type ToolCtx struct {
	App       *App
	SessionID string
	JobID     string
}

// vars is what the calling conversation holds (ADR-068). A call outside one
// holds nothing.
func (tc *ToolCtx) vars() map[string]string {
	if tc == nil || tc.App == nil || tc.App.store == nil || tc.SessionID == "" {
		return nil
	}
	return tc.App.store.SessionEnv(tc.SessionID)
}

type LoadFailure struct {
	Dir    string `json:"dir"`
	Reason string `json:"reason"`
}

type Registry struct {
	db       *sql.DB
	mu       sync.RWMutex
	tools    map[string]*Tool
	order    []string
	failures []LoadFailure
	dir      string
	dbPath   string
	applied  map[string]bool
}

func NewRegistry(dir, dbPath string, db *sql.DB) *Registry {
	return &Registry{tools: map[string]*Tool{}, dir: dir, dbPath: dbPath, db: db, applied: map[string]bool{}}
}

func (r *Registry) register(t *Tool) {
	if _, ok := r.tools[t.Name]; !ok {
		r.order = append(r.order, t.Name)
	}
	t.LoadedAt = time.Now()
	r.tools[t.Name] = t
}

func (r *Registry) Get(name string) *Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

func (r *Registry) All() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Tool, 0, len(r.tools))
	for _, n := range r.order {
		out = append(out, r.tools[n])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *Registry) Failures() []LoadFailure {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]LoadFailure(nil), r.failures...)
}

// Load scans the tool directory. A tool that fails validation is not registered
// and previously loaded tools keep working.
func (r *Registry) Load(app *App) ([]string, []LoadFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = nil
	var loaded []string

	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, []LoadFailure{{Dir: r.dir, Reason: err.Error()}}
	}
	prefixes := map[string]string{}
	for _, t := range r.tools {
		if t.Agent.DBPrefix != "" {
			prefixes[t.Agent.DBPrefix] = t.Name
		}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(r.dir, e.Name())
		t, err := loadToolDir(dir)
		if err != nil {
			r.failures = append(r.failures, LoadFailure{Dir: e.Name(), Reason: err.Error()})
			continue
		}
		if owner, ok := prefixes[t.Agent.DBPrefix]; ok && owner != t.Name {
			r.failures = append(r.failures, LoadFailure{Dir: e.Name(),
				Reason: fmt.Sprintf("db_prefix %q already used by %s", t.Agent.DBPrefix, owner)})
			continue
		}
		if err := r.applySchema(t); err != nil {
			r.failures = append(r.failures, LoadFailure{Dir: e.Name(), Reason: "schema.sql: " + err.Error()})
			continue
		}
		prefixes[t.Agent.DBPrefix] = t.Name
		r.register(t)
		loaded = append(loaded, t.Name)
	}
	return loaded, r.failures
}

func loadToolDir(dir string) (*Tool, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if err := strayField(b); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if m.Name == "" || m.Description == "" || m.Agent.DBPrefix == "" || m.Parameters == nil {
		return nil, fmt.Errorf("manifest missing name, description, parameters, or agent.db_prefix")
	}
	t := Tool{Name: m.Name, Description: m.Description, Parameters: m.Parameters, Agent: m.Agent}
	if typ, _ := t.Parameters["type"].(string); typ != "object" {
		return nil, fmt.Errorf("parameters must be a JSON Schema object")
	}
	run := filepath.Join(dir, "run")
	st, err := os.Stat(run)
	if err != nil {
		return nil, fmt.Errorf("run: %w", err)
	}
	if st.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("run is not executable")
	}
	if t.Agent.Timeout <= 0 {
		t.Agent.Timeout = 30
	}
	_, err = os.Stat(filepath.Join(dir, "ui", "panel.js"))
	t.HasPanel = err == nil
	t.Dir = dir
	return &t, nil
}

// strayField names the first field a manifest does not define. A setting at the
// top level would sit beside what the model is sent, and one misspelt under
// "agent" would be ignored for its default; either is refused when the tool
// loads, rather than noticed at the call it was meant to change.
func strayField(b []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return err
	}
	if err := onlyFields(top, "name", "description", "parameters", "agent"); err != nil {
		return fmt.Errorf("%w; the top level is what the model is sent, and the agent's own settings go under \"agent\"", err)
	}
	raw, ok := top["agent"]
	if !ok {
		return nil
	}
	var agent map[string]json.RawMessage
	if err := json.Unmarshal(raw, &agent); err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	if err := onlyFields(agent, "db_prefix", "timeout_seconds", "reads", "env"); err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	return nil
}

func onlyFields(m map[string]json.RawMessage, known ...string) error {
	var stray []string
	for k := range m {
		if !slices.Contains(known, k) {
			stray = append(stray, k)
		}
	}
	if len(stray) == 0 {
		return nil
	}
	sort.Strings(stray)
	return fmt.Errorf("%q is not a manifest field", stray[0])
}

func (r *Registry) applySchema(t *Tool) error {
	path := filepath.Join(t.Dir, "schema.sql")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	key := t.Name + ":" + fmt.Sprint(len(b))
	if r.applied[key] {
		return nil
	}
	if _, err := r.db.Exec(string(b)); err != nil {
		return err
	}
	r.applied[key] = true
	return nil
}

// SchemasFor returns the tool definitions a session may use, exactly as the
// model receives them. A session's enabled set is fixed from its first turn, so the
// prompt can carry precisely the callable tools and nothing else needs to
// enforce availability.
func (r *Registry) SchemasFor(sess *Session) []ToolSchema {
	out := []ToolSchema{}
	for _, t := range r.All() {
		if sess != nil && !sess.toolEnabled(t.Name) {
			continue
		}
		out = append(out, t.schema())
	}
	return out
}

// schema is the tool as the model is sent it: the manifest's standard part, and
// nothing from its agent block. The Tools screen shows this same value.
func (t *Tool) schema() ToolSchema {
	return ToolSchema{Type: "function", Function: ToolSchemaFn{
		Name: t.Name, Description: t.Description, Parameters: t.Parameters}}
}

type toolResult struct {
	OK      bool   `json:"ok"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
	stderr  string
}

func errResult(format string, a ...any) toolResult {
	return toolResult{OK: false, Error: fmt.Sprintf(format, a...)}
}

// Call runs a tool. A crash, timeout, or unparseable output becomes an error
// result the model can read, never a failed turn.
func (r *Registry) Call(ctx context.Context, tc *ToolCtx, name string, args json.RawMessage) toolResult {
	t := r.Get(name)
	if t == nil {
		return errResult("no tool named %q", name)
	}
	if t.Builtin {
		out, err := t.run(ctx, tc, args)
		if err != nil {
			return errResult("%s", err.Error())
		}
		switch v := out.(type) {
		case string:
			return toolResult{OK: true, Content: v}
		default:
			b, _ := json.Marshal(v)
			return toolResult{OK: true, Content: string(b)}
		}
	}

	cctx, cancel := context.WithTimeout(ctx, time.Duration(t.Agent.Timeout)*time.Second)
	defer cancel()
	bin, err := filepath.Abs(filepath.Join(t.Dir, "run"))
	if err != nil {
		return errResult("tool %q: %v", name, err)
	}
	// A tool runs in the working directory of the session that called it, so a
	// relative path in a tool call means that conversation's own files — and,
	// where the operating system can enforce it, that directory is the only one
	// it can reach.
	workspace := tc.App.ensureWorkspace(tc.SessionID)
	// Whatever the tool did, it did it here: what the directory holds is
	// measured again rather than read from what was kept.
	defer tc.App.workspaceChanged(tc.SessionID)
	reads := append(append([]string(nil), t.Agent.Reads...), tc.App.skillFolders(tc.SessionID)...)
	argv := tc.App.sandbox.Wrap(bin, workspace, r.dir, reads, nil)
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = workspace
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	// The system temporary directory is not writable, so a tool is given one
	// inside its own working directory. Created here rather than by the tool,
	// because an interpreter reaches for it before any tool code runs.
	tmp := tc.App.sandbox.TempDir(workspace)
	_ = os.MkdirAll(tmp, 0o755)
	// The same for the home directory: the container user's is granted nothing,
	// and a program that keeps state there fails on a path it never names.
	home := tc.App.sandbox.HomeDir(workspace)
	_ = os.MkdirAll(home, 0o755)
	cmd.Stdin = strings.NewReader(string(args))
	// The token is this call's, and dies with it: the tool API answers a
	// request only for a call that is running, as the conversation it runs in.
	token, done := tc.App.calls.issue(tc.SessionID, tc.JobID)
	defer done()
	// What a program needs to start, what the manifest names, then what the
	// conversation holds, which takes the place of a manifest's variable of the
	// same name; the contract goes last, so nothing stands in for it.
	cmd.Env = append(toolBaseEnv(), declaredEnv(t.Agent.Env)...)
	cmd.Env = append(cmd.Env, sessionVars(tc.vars())...)
	cmd.Env = append(cmd.Env,
		"AGENT_DB="+r.dbPath,
		"AGENT_DB_PREFIX="+t.Agent.DBPrefix,
		"AGENT_URL="+tc.App.toolURL(),
		"AGENT_TOKEN="+token,
		"AGENT_WORKSPACE="+workspace,
		"AGENT_SESSION="+tc.SessionID,
		"AGENT_JOB="+tc.JobID,
		"TMPDIR="+tmp,
		"HOME="+home)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if cctx.Err() == context.DeadlineExceeded {
		res := errResult("tool %q exceeded its %ds timeout", name, t.Agent.Timeout)
		res.stderr = stderr.String()
		return res
	}
	if err != nil {
		res := errResult("tool %q failed: %v", name, err)
		res.stderr = stderr.String()
		return res
	}
	var res toolResult
	if err := json.Unmarshal(stdout, &res); err != nil {
		res = errResult("tool %q returned unparseable output: %s", name, truncate(string(stdout), 400))
	}
	res.stderr = stderr.String()
	return res
}

// toolPassthrough is what a subprocess needs to start at all: where to find its
// interpreter and its libraries, where its user's home is, how to talk about
// text, and which certificates to trust. Nothing here is a credential.
// HOME is not among them: the agent's own home directory is granted to no
// tool, so passing it through hands every program a path it cannot use. It is
// set per call, beside TMPDIR, to a directory inside the session's own.
var toolPassthrough = []string{
	"PATH", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "TERM",
}

// neverDeclared is the one name a manifest cannot ask for. The allow-list
// exists because this variable reached every tool, including the one that runs
// commands the model composed; a manifest that could hand it back would undo the
// decision rather than refine it.
var neverDeclared = map[string]bool{"OPENROUTER_API_KEY": true}

// toolBaseEnv builds the environment a tool is started with. A subprocess used
// to inherit the agent's whole environment, which handed the model API key to
// every tool including the one that runs shell commands the model wrote. The
// list is an allow-list for the same reason the sandbox is one: a variable
// nobody considered is absent rather than present.
func toolBaseEnv() []string {
	var out []string
	for _, name := range toolPassthrough {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// declaredEnv is what a tool's manifest asked for, from the agent's own
// environment. A name that is not set is absent rather than empty, the way a
// grant is: a tool asks whether it has a credential by asking whether the
// variable is there. The model key is refused however it is asked for — a
// manifest is reviewed by a person before it ships, but the one variable the
// whole allow-list exists to keep out is not opened by review.
func declaredEnv(names []string) []string {
	var out []string
	for _, name := range names {
		if neverDeclared[name] || toolContractEnv[name] {
			continue
		}
		if v, ok := os.LookupEnv(name); ok && v != "" {
			out = append(out, name+"="+v)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
