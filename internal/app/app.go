package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Addr      string
	DataDir   string
	Workspace string
	ToolsDir  string
	SkillsDir string
	WebDir    string
	EnvFile   string
	// ChangelogPath is the file the version screen reads. It ships with the
	// app rather than being derived: the container has no repository.
	ChangelogPath string
	// ReadPaths are directories the operator adds to what a tool may read,
	// beyond the runtime. A browser installed outside the system roots is the
	// case it exists for. It only adds; nothing here removes a boundary.
	ReadPaths string
	// ToolPorts are TCP ports the operator adds to the ones a tool may connect
	// to. Like ReadPaths it only adds; the operator's own port is never added.
	ToolPorts          string
	CompactAtTokens    int
	KeepVerbatimTokens int
	SummaryEvery       int
	MemoryCapacity     int
	APIKey             string
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// loadEnvFile fills environment variables from a file of KEY=VALUE lines, so
// secrets and settings live somewhere durable instead of in a shell the operator
// has to remember to prepare. A variable already set in the environment always
// wins, which keeps a one-off override on the command line working.
//
// The file is optional. Blank lines and # comments are skipped, a leading
// "export " is tolerated, and a value may be wrapped in single or double quotes.
func loadEnvFile(path string) {
	lines, err := readEnvFile(path)
	if err != nil {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		log.Printf("warning: %s is readable by other users; chmod 600 it", path)
	}
	n := 0
	for _, l := range lines {
		if _, set := os.LookupEnv(l.key); set {
			continue
		}
		if os.Setenv(l.key, l.value) == nil {
			n++
		}
	}
	if n > 0 {
		log.Printf("config: %d variables from %s", n, path)
	}
}

type envLine struct{ key, value string }

// readEnvFile parses a file of KEY=VALUE lines without applying it. Loading the
// environment and listing what could be granted read the file the same way.
func readEnvFile(path string) ([]envLine, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []envLine
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) > 1 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if k == "" {
			continue
		}
		out = append(out, envLine{k, v})
	}
	return out, nil
}

// The layout is two roots, not seven paths. State is what outlives the
// container and is the one directory a deployment mounts; home is what the
// image ships and never changes. Both default to the working directory, which
// is what running from a checkout has always meant.
//
// There were seven settings here, and nothing ever set them except the image —
// where they always resolved to these two roots. Seven lines to keep in step
// with one layout decision is six chances to get it wrong, for a choice nobody
// wanted to make separately.
func LoadConfig() Config {
	state := envOr("AGENT_STATE", ".")
	home := envOr("AGENT_HOME", ".")
	// Read before the file is loaded, so the roots come from the real
	// environment. A settings file cannot say where it lives.
	envFile := filepath.Join(state, ".env")
	loadEnvFile(envFile)
	return Config{
		EnvFile: envFile,
		// Not 8080: that is a port tools commonly use, and a tool may not reach
		// the operator's. This one nothing else commonly takes.
		Addr:          envOr("AGENT_ADDR", ":7770"),
		ToolPorts:     os.Getenv("AGENT_TOOL_PORTS"),
		DataDir:       filepath.Join(state, "data"),
		Workspace:     filepath.Join(state, "workspace"),
		ToolsDir:      filepath.Join(home, "tools"),
		SkillsDir:     filepath.Join(home, "skills"),
		WebDir:        filepath.Join(home, "web"),
		ChangelogPath: filepath.Join(home, "CHANGELOG.md"),
		ReadPaths:     os.Getenv("AGENT_READ_PATHS"),
		// The model a conversation starts on is not a setting either: it is the
		// one last chosen on screen, kept beside the data (see startingModel).
		//
		// Not settings. Nothing ever set them, and the first two were defaults
		// for a default: a session carries its own compact_at_tokens and
		// keep_verbatim_tokens, editable on the screen it is read from.
		CompactAtTokens:    defaultCompactAtTokens,
		KeepVerbatimTokens: defaultKeepVerbatimTokens,
		SummaryEvery:       defaultSummaryEvery,
		MemoryCapacity:     defaultMemoryCapacity,
		APIKey:             os.Getenv("OPENROUTER_API_KEY"),
	}
}

type App struct {
	cfg     Config
	sandbox *Sandbox
	store   *Store
	tools   *Registry
	skills  *Skills
	or      *OpenRouter
	hub     *Hub
	sched   *Scheduler

	// calls are the tool calls running now, which the tool API answers; toolAddr
	// is where it listens.
	calls    callTokens
	toolAddr string

	qmu    sync.Mutex
	queues map[string]chan func()
	busy   map[string]bool
}

func Run() error {
	cfg := LoadConfig()
	if cfg.APIKey == "" {
		log.Println("warning: OPENROUTER_API_KEY is not set; model calls will fail")
	}
	for _, d := range []string{cfg.DataDir, cfg.Workspace, cfg.ToolsDir, cfg.SkillsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	st, err := OpenStore(cfg.DataDir)
	if err != nil {
		return err
	}
	dbPath, err := filepath.Abs(DBPath(cfg.DataDir))
	if err != nil {
		return err
	}
	// Before any tool can run: the agent's own environment holds the model key,
	// and a tool granted /proc could otherwise read it out of /proc/<pid>/environ.
	if err := hideProcess(); err != nil {
		log.Printf("warning: could not hide this process's environment from other processes: %v", err)
	}
	sandbox := NewSandbox(cfg)
	a := &App{
		cfg:     cfg,
		sandbox: sandbox,
		store:   st,
		tools:   NewRegistry(cfg.ToolsDir, dbPath, st.DB()),
		skills:  NewSkills(cfg.SkillsDir),
		or:      NewOpenRouter(cfg.APIKey),
		hub:     NewHub(),
		queues:  map[string]chan func(){},
		busy:    map[string]bool{},
	}
	// Before any tool can run, and before the sandbox is described: the tool
	// API's port is part of the policy.
	stopTools, err := a.listenTools()
	if err != nil {
		return err
	}
	defer stopTools()
	log.Printf("tool API on %s", a.toolAddr)
	log.Print(sandbox.Describe())
	a.registerBuiltins()
	loaded, failures := a.tools.Load(a)
	log.Printf("tools: %d builtin, %d from disk, %d failed", len(a.tools.All())-len(loaded), len(loaded), len(failures))

	a.sched = NewScheduler(a)
	go a.sched.Run(context.Background())

	mux := a.routes()
	log.Printf("agent listening on %s (workspace %s)", cfg.Addr, cfg.Workspace)
	return http.ListenAndServe(cfg.Addr, mux)
}

// ReloadTools validates and registers tools from disk. A newly registered tool is
// not in the prompt of any session already running, so it takes effect in the
// sessions started after it, the way a written memory does. The calling session
// gets an entry saying so, which is a visible record rather than something the
// model is told.
func (a *App) ReloadTools(sessionID string) ([]string, []LoadFailure) {
	before := map[string]bool{}
	for _, t := range a.tools.All() {
		before[t.Name] = true
	}
	loaded, failures := a.tools.Load(a)
	var added []string
	for _, n := range loaded {
		if !before[n] {
			added = append(added, n)
		}
	}
	_ = added // the reload result names what was added; nothing writes it twice
	return loaded, failures
}

// ---- per-session serialisation ----

// enqueue runs fn on the session's single-turn queue. Two turns never run
// concurrently in one session, and nothing is reordered.
func (a *App) enqueue(sessionID string, fn func()) {
	a.qmu.Lock()
	q, ok := a.queues[sessionID]
	if !ok {
		q = make(chan func(), 64)
		a.queues[sessionID] = q
		go func() {
			for f := range q {
				a.qmu.Lock()
				a.busy[sessionID] = true
				a.qmu.Unlock()
				f()
				a.qmu.Lock()
				a.busy[sessionID] = false
				a.qmu.Unlock()
			}
		}()
	}
	a.qmu.Unlock()
	q <- fn
}

// Busy reports whether a turn is running or queued for this session.
func (a *App) Busy(sessionID string) bool {
	a.qmu.Lock()
	defer a.qmu.Unlock()
	return a.busy[sessionID] || len(a.queues[sessionID]) > 0
}

func newID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	// ULID-ish: 48-bit timestamp, sortable by creation time.
	ts := uint64(time.Now().UnixMilli())
	head := fmt.Sprintf("%010X", ts)
	return head + strings.ToUpper(hex.EncodeToString(b))[:10]
}
