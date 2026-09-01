package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Addr            string
	DataDir         string
	Workspace       string
	ToolsDir        string
	SkillsDir       string
	WebDir          string
	RepoRoot        string
	DefaultModel    string
	RotateAtTokens  int
	CarryOverTokens int
	SummaryEvery    int
	MemoryCapacity  int
	APIKey          string
	VAPIDPublic     string
	VAPIDPrivate    string
	VAPIDSubject    string
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

func LoadConfig() Config {
	return Config{
		Addr:            envOr("AGENT_ADDR", ":8080"),
		DataDir:         envOr("AGENT_DATA", "data"),
		Workspace:       envOr("AGENT_WORKSPACE", "workspace"),
		ToolsDir:        envOr("AGENT_TOOLS", "tools"),
		SkillsDir:       envOr("AGENT_SKILLS", "skills"),
		WebDir:          envOr("AGENT_WEB", "web"),
		RepoRoot:        envOr("AGENT_REPO", "."),
		DefaultModel:    envOr("AGENT_MODEL", "anthropic/claude-sonnet-4.5"),
		RotateAtTokens:  envInt("AGENT_ROTATE_TOKENS", 40000),
		CarryOverTokens: envInt("AGENT_CARRY_TOKENS", 5000),
		SummaryEvery:    envInt("AGENT_SUMMARY_EVERY", 4000),
		MemoryCapacity:  envInt("AGENT_MEMORY_CAPACITY", 8000),
		APIKey:          os.Getenv("OPENROUTER_API_KEY"),
		VAPIDPublic:     os.Getenv("AGENT_VAPID_PUBLIC"),
		VAPIDPrivate:    os.Getenv("AGENT_VAPID_PRIVATE"),
		VAPIDSubject:    envOr("AGENT_VAPID_SUBJECT", "mailto:operator@localhost"),
	}
}

type App struct {
	cfg    Config
	store  *Store
	tools  *Registry
	skills *Skills
	or     *OpenRouter
	hub    *Hub
	sched  *Scheduler

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
	a := &App{
		cfg:    cfg,
		store:  st,
		tools:  NewRegistry(cfg.ToolsDir, dbPath, st.DB()),
		skills: NewSkills(cfg.SkillsDir),
		or:     NewOpenRouter(cfg.APIKey),
		hub:    NewHub(),
		queues: map[string]chan func(){},
		busy:   map[string]bool{},
	}
	a.registerBuiltins()
	loaded, failures := a.tools.Load(a)
	log.Printf("tools: %d builtin, %d from disk, %d failed", len(a.tools.All())-len(loaded), len(loaded), len(failures))
	a.initGit()

	a.sched = NewScheduler(a)
	go a.sched.Run(context.Background())

	mux := a.routes()
	log.Printf("agent listening on %s (workspace %s)", cfg.Addr, cfg.Workspace)
	return http.ListenAndServe(cfg.Addr, mux)
}

// git runs a git command in the repository root, which holds the workspace,
// the tools, and the skills.
func (a *App) git(args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = a.cfg.RepoRoot
	_ = cmd.Run()
}

// initGit makes the working directory a git repository so a tool that breaks
// the system is reverted rather than reconstructed.
func (a *App) initGit() {
	if _, err := os.Stat(filepath.Join(a.cfg.RepoRoot, ".git")); err != nil {
		a.git("init", "-q")
	}
	gitignore := filepath.Join(a.cfg.RepoRoot, ".gitignore")
	if _, err := os.Stat(gitignore); err != nil {
		_ = os.WriteFile(gitignore, []byte("data/\n"), 0o644)
	}
	a.git("add", "-A")
	a.git("-c", "user.email=agent@localhost", "-c", "user.name=agent",
		"commit", "-q", "-m", "initial", "--allow-empty")
}

// commitTools records the tool directory's current state after a reload.
func (a *App) commitTools(msg string) {
	target := a.cfg.ToolsDir
	if rel, err := filepath.Rel(a.cfg.RepoRoot, a.cfg.ToolsDir); err == nil && !strings.HasPrefix(rel, "..") {
		target = rel
	}
	a.git("add", "-A", "--", target)
	a.git("-c", "user.email=agent@localhost", "-c", "user.name=agent", "commit", "-q", "-m", msg)
}

// ReloadTools validates and registers tools from disk. A newly registered tool
// becomes callable in the current session by appending its definition, without
// invalidating the cached prefix.
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
	if len(failures) == 0 {
		a.commitTools("tools: reload")
	}
	if sessionID != "" {
		for _, n := range added {
			t := a.tools.Get(n)
			a.appendEvent(sessionID, Entry{EventKind: "tool_added",
				Text: fmt.Sprintf("tool added: %s — %s", t.Name, t.Description)})
		}
	}
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
