package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

// Tool is either a builtin or one a bundle's server offers.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	// Agent is what the tool's bundle says of how it is run. A builtin runs
	// inside the agent and leaves it empty.
	Agent    ToolAgent
	LoadedAt time.Time
	Builtin  bool
	Dir      string
	run      builtinFn
	// bundle is the MCP bundle whose server offers this tool, or nil for a
	// builtin (ADR-085).
	bundle *bundle
	// app is the page the tool's result opens and who may call the tool
	// (ADR-090). A builtin has no page, and is the model's to call.
	app toolApp
}

// ToolAgent is what a bundle's manifest says under _meta for this agent:
// everything the agent reads to run the tool, and never sends to the model.
type ToolAgent struct {
	Timeout int `json:"timeout_seconds"`
	// Reads names the paths this tool may read beyond the runtime every tool
	// gets. A browser reads /proc and /sys before it renders anything; a tool
	// that edits a file does not, and granting the union of what any tool might
	// need would hand every tool the widest boundary any of them asks for.
	Reads []string `json:"reads,omitempty"`
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
	mu       sync.RWMutex
	tools    map[string]*Tool
	order    []string
	failures []LoadFailure
	// bundleDirs are the directories bundles are unpacked in: what the image
	// ships, then what the operator installed.
	bundleDirs []string
	// plugins is every bundle found there at the last load, loaded or not.
	plugins []Plugin
}

func NewRegistry(bundleDirs ...string) *Registry {
	return &Registry{tools: map[string]*Tool{}, bundleDirs: bundleDirs}
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

// Plugins is every bundle found at the last load, whether or not it loaded.
func (r *Registry) Plugins() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Plugin(nil), r.plugins...)
}

func (r *Registry) Failures() []LoadFailure {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]LoadFailure(nil), r.failures...)
}

// Load reads the bundle directories. What is on disk is read afresh: a bundle
// that fails validation is not registered, one that has gone is no longer
// offered, and the others keep working.
func (r *Registry) Load(app *App) ([]string, []LoadFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = nil
	r.order = slices.DeleteFunc(r.order, func(name string) bool {
		if r.tools[name].Builtin {
			return false
		}
		delete(r.tools, name)
		return true
	})
	return r.loadBundles(app), r.failures
}

// SchemasFor returns the tool definitions a session may use, exactly as the
// model receives them. A session's enabled set is fixed from its first turn, so the
// prompt can carry precisely the callable tools and nothing else needs to
// enforce availability.
func (r *Registry) SchemasFor(sess *Session) []ToolSchema {
	out := []ToolSchema{}
	for _, t := range r.All() {
		// A tool that is only a page's to call is not the model's to know of.
		if !t.Builtin && !t.app.forModel {
			continue
		}
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
	// app is the result as the bundle's server answered it, and page the same
	// when the tool has a page to hand it to.
	app, page json.RawMessage
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

	return r.callBundle(ctx, tc, t, args)
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

// neverDeclared is the one name a manifest cannot set. The allow-list
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

// truncate cuts s to at most n bytes and says that it did. The cut falls
// between characters: one that fell inside a character left half of it, which
// is not text, and reached the screen as a replacement mark at the end of a
// title.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
