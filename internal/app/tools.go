package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tool is either a builtin or a directory containing a manifest and an executable.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	DBPrefix    string         `json:"db_prefix"`
	Timeout     int            `json:"timeout_seconds"`
	Parameters  map[string]any `json:"parameters"`
	// Env names the environment variables this tool receives beyond the ones
	// every tool is promised. It is how a tool that needs a credential asks for
	// one, and it is the only way any variable of the agent's own environment
	// reaches a subprocess.
	Env []string `json:"env,omitempty"`
	// Reads names the paths this tool may read beyond the runtime every tool
	// gets. A browser reads /proc and /sys before it renders anything; a tool
	// that edits a file does not, and granting the union of what any tool might
	// need would hand every tool the widest boundary any of them asks for.
	Reads    []string  `json:"reads,omitempty"`
	HasPanel bool      `json:"has_panel"`
	LoadedAt time.Time `json:"loaded_at"`
	Builtin  bool      `json:"builtin"`
	Dir      string    `json:"-"`
	run      builtinFn `json:"-"`
}

type builtinFn func(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error)

// ToolCtx is what a tool call knows about where it is running.
type ToolCtx struct {
	App       *App
	SessionID string
	JobID     string
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
		if t.DBPrefix != "" {
			prefixes[t.DBPrefix] = t.Name
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
		if owner, ok := prefixes[t.DBPrefix]; ok && owner != t.Name {
			r.failures = append(r.failures, LoadFailure{Dir: e.Name(),
				Reason: fmt.Sprintf("db_prefix %q already used by %s", t.DBPrefix, owner)})
			continue
		}
		if err := r.applySchema(t); err != nil {
			r.failures = append(r.failures, LoadFailure{Dir: e.Name(), Reason: "schema.sql: " + err.Error()})
			continue
		}
		prefixes[t.DBPrefix] = t.Name
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
	var t Tool
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if t.Name == "" || t.Description == "" || t.DBPrefix == "" || t.Parameters == nil {
		return nil, fmt.Errorf("manifest missing name, description, db_prefix, or parameters")
	}
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
	if t.Timeout <= 0 {
		t.Timeout = 30
	}
	_, err = os.Stat(filepath.Join(dir, "ui", "panel.js"))
	t.HasPanel = err == nil
	t.Dir = dir
	return &t, nil
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
		out = append(out, ToolSchema{Type: "function", Function: ToolSchemaFn{
			Name: t.Name, Description: t.Description, Parameters: t.Parameters}})
	}
	return out
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

	cctx, cancel := context.WithTimeout(ctx, time.Duration(t.Timeout)*time.Second)
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
	argv := tc.App.sandbox.Wrap(bin, workspace, r.dir, t.Reads, nil)
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
	cmd.Stdin = strings.NewReader(string(args))
	cmd.Env = append(toolBaseEnv(t.Env),
		"AGENT_DB="+r.dbPath,
		"AGENT_DB_PREFIX="+t.DBPrefix,
		"AGENT_URL="+tc.App.cfg.BaseURL(),
		"AGENT_WORKSPACE="+workspace,
		"AGENT_SESSION="+tc.SessionID,
		"AGENT_JOB="+tc.JobID,
		"TMPDIR="+tmp)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if cctx.Err() == context.DeadlineExceeded {
		res := errResult("tool %q exceeded its %ds timeout", name, t.Timeout)
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
var toolPassthrough = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "TERM",
}

// toolBaseEnv builds the environment a tool is started with. A subprocess used
// to inherit the agent's whole environment, which handed the model API key to
// every tool including the one that runs shell commands the model wrote. The
// list is an allow-list for the same reason the sandbox is one: a variable
// nobody considered is absent rather than present.
func toolBaseEnv(extra []string) []string {
	var out []string
	for _, name := range append(append([]string{}, toolPassthrough...), extra...) {
		if v, ok := os.LookupEnv(name); ok {
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
