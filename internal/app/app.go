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
	// ReadPaths are directories the operator adds to what a tool may read,
	// beyond the runtime. A browser installed outside the system roots is the
	// case it exists for. It only adds; nothing here removes a boundary.
	ReadPaths       string
	DefaultModel    string
	RotateAtTokens  int
	CarryOverTokens int
	SummaryEvery    int
	MemoryCapacity  int
	APIKey          string
}

// BaseURL is the address a tool uses to reach the API. Tools run beside the
// gateway in the same container, so this is always the loopback address: a tool
// asks the system for what it needs the same way the interface does.
func (c Config) BaseURL() string {
	addr := c.Addr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	return "http://" + addr
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
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		log.Printf("warning: %s is readable by other users; chmod 600 it", path)
	}
	n := 0
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
		if _, set := os.LookupEnv(k); set {
			continue
		}
		if os.Setenv(k, v) == nil {
			n++
		}
	}
	if n > 0 {
		log.Printf("config: %d variables from %s", n, path)
	}
}

func LoadConfig() Config {
	envFile := envOr("AGENT_ENV", ".env")
	loadEnvFile(envFile)
	return Config{
		EnvFile:         envFile,
		Addr:            envOr("AGENT_ADDR", ":8080"),
		DataDir:         envOr("AGENT_DATA", "data"),
		Workspace:       envOr("AGENT_WORKSPACE", "workspace"),
		ToolsDir:        envOr("AGENT_TOOLS", "tools"),
		ReadPaths:       os.Getenv("AGENT_READ_PATHS"),
		SkillsDir:       envOr("AGENT_SKILLS", "skills"),
		WebDir:          envOr("AGENT_WEB", "web"),
		DefaultModel:    envOr("AGENT_MODEL", "anthropic/claude-sonnet-4.5"),
		RotateAtTokens:  envInt("AGENT_ROTATE_TOKENS", 40000),
		CarryOverTokens: envInt("AGENT_CARRY_TOKENS", 5000),
		SummaryEvery:    envInt("AGENT_SUMMARY_EVERY", 4000),
		MemoryCapacity:  envInt("AGENT_MEMORY_CAPACITY", 8000),
		APIKey:          os.Getenv("OPENROUTER_API_KEY"),
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
	dbPath, err := filepath.Abs(filepath.Join(cfg.DataDir, "agent.db"))
	if err != nil {
		return err
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
