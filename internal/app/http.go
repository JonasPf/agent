package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxUpload = 100 << 20

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /sessions", a.hSessions)
	mux.HandleFunc("POST /sessions", a.hCreateSession)
	mux.HandleFunc("GET /sessions/{id}", a.hSession)
	mux.HandleFunc("PATCH /sessions/{id}", a.hPatchSession)
	mux.HandleFunc("DELETE /sessions/{id}", a.hDeleteSession)
	mux.HandleFunc("GET /sessions/{id}/transcript", a.hTranscript)
	mux.HandleFunc("POST /sessions/{id}/messages", a.hSendMessage)
	mux.HandleFunc("POST /sessions/{id}/fork", a.hFork)
	mux.HandleFunc("POST /sessions/{id}/read", a.hMarkRead)
	mux.HandleFunc("GET /search", a.hSearch)

	mux.HandleFunc("GET /jobs", a.hJobs)
	mux.HandleFunc("POST /jobs", a.hCreateJob)
	mux.HandleFunc("GET /jobs/{id}", a.hJob)
	mux.HandleFunc("PATCH /jobs/{id}", a.hPatchJob)
	mux.HandleFunc("DELETE /jobs/{id}", a.hDeleteJob)

	mux.HandleFunc("GET /dead-letters", a.hDeadLetters)
	mux.HandleFunc("POST /dead-letters/{id}/replay", a.hReplay)
	mux.HandleFunc("POST /dead-letters/{id}/dismiss", a.hDismiss)

	mux.HandleFunc("GET /memory", a.hMemory)
	mux.HandleFunc("POST /memory", a.hAddMemory)
	mux.HandleFunc("PATCH /memory/{id}", a.hPatchMemory)
	mux.HandleFunc("DELETE /memory/{id}", a.hDeleteMemory)

	mux.HandleFunc("GET /tools", a.hTools)
	mux.HandleFunc("POST /tools/reload", a.hReload)
	mux.HandleFunc("GET /tools/{name}/panel.js", a.hPanel)
	mux.HandleFunc("POST /tools/{name}/call", a.hCallTool)

	mux.HandleFunc("GET /skills", a.hSkills)
	mux.HandleFunc("GET /skills/{name}", a.hSkill)

	mux.HandleFunc("POST /uploads", a.hUpload)
	mux.HandleFunc("GET /push/key", a.hPushKey)
	mux.HandleFunc("POST /push/subscriptions", a.hPushSubscribe)
	mux.HandleFunc("GET /models", a.hModels)
	mux.HandleFunc("GET /status", a.hStatus)
	mux.HandleFunc("/ws", a.handleWS)

	mux.Handle("/", http.FileServer(http.Dir(a.cfg.WebDir)))
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, format string, args ...any) {
	writeJSON(w, code, map[string]string{"error": fmt.Sprintf(format, args...)})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<24)).Decode(v)
}

// enrich fills the derived fields the interface shows.
func (a *App) enrich(s *Session) *Session {
	out := *s
	entries := a.store.Entries(s.ID)
	out.EntryCount = len(entries)
	out.ContextUsed = projectedTokens(entries)
	if jobs, err := a.store.SessionJobs(s.ID); err == nil {
		out.JobCount = len(jobs)
	}
	if s.PromptTokens > 0 {
		out.CacheHitRate = float64(s.CachedTokens) / float64(s.PromptTokens)
	}
	return &out
}

func (a *App) hSessions(w http.ResponseWriter, r *http.Request) {
	all := a.store.Sessions()
	out := make([]*Session, 0, len(all))
	for _, s := range all {
		out = append(out, a.enrich(s))
	}
	writeJSON(w, 200, out)
}

func (a *App) hCreateSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model string `json:"model"`
		From  string `json:"from"`
	}
	_ = readJSON(r, &in)
	var seed *Session
	if in.From != "" {
		seed = a.store.Session(in.From)
	}
	s, err := a.NewSession(in.Model, seed)
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	writeJSON(w, 201, a.enrich(s))
}

func (a *App) hSession(w http.ResponseWriter, r *http.Request) {
	s := a.store.Session(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "no such session")
		return
	}
	out := a.enrich(s)
	// A link to an archived session resolves to the live session of its chain.
	if live := a.LiveSession(s.ID); live != nil && live.ID != s.ID {
		writeJSON(w, 200, map[string]any{"session": out, "redirected_to": live.ID})
		return
	}
	writeJSON(w, 200, map[string]any{"session": out})
}

func (a *App) hPatchSession(w http.ResponseWriter, r *http.Request) {
	s := a.store.Session(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "no such session")
		return
	}
	var in struct {
		Title         *string   `json:"title"`
		Model         *string   `json:"model"`
		Status        *string   `json:"status"`
		Muted         *bool     `json:"muted"`
		Summary       *string   `json:"summary"`
		EnabledTools  *[]string `json:"enabled_tools"`
		EnabledSkills *[]string `json:"enabled_skills"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if in.Title != nil {
		s.Title = *in.Title
	}
	if in.Model != nil && *in.Model != s.Model {
		old := s.Model
		s.Model = *in.Model
		a.appendEvent(s.ID, Entry{EventKind: "model_change",
			Text: fmt.Sprintf("model changed from %s to %s", old, s.Model)})
	}
	if in.Status != nil {
		s.Status = *in.Status
	}
	if in.Muted != nil {
		s.Muted = *in.Muted
	}
	if in.Summary != nil {
		s.Summary = *in.Summary
	}
	if in.EnabledTools != nil || in.EnabledSkills != nil {
		var t, k []string
		if in.EnabledTools != nil {
			t = *in.EnabledTools
		}
		if in.EnabledSkills != nil {
			k = *in.EnabledSkills
		}
		a.SetAvailability(s, t, k, in.EnabledTools != nil, in.EnabledSkills != nil)
	}
	_ = a.store.PutSession(s)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	writeJSON(w, 200, a.enrich(s))
}

func (a *App) hDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteSession(r.PathValue("id")); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	w.WriteHeader(204)
}

func (a *App) hTranscript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.store.Session(id) == nil {
		fail(w, 404, "no such session")
		return
	}
	entries := a.store.Entries(id)
	if from := r.URL.Query().Get("from"); from != "" {
		n, _ := strconv.Atoi(from)
		if n > 0 && n < len(entries) {
			entries = entries[n:]
		}
	}
	writeJSON(w, 200, entries)
}

func (a *App) hSendMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		fail(w, 400, "text is required")
		return
	}
	if err := a.SendUserMessage(r.PathValue("id"), in.Text); err != nil {
		fail(w, 404, "%v", err)
		return
	}
	w.WriteHeader(202)
}

func (a *App) hFork(w http.ResponseWriter, r *http.Request) {
	s := a.store.Session(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "no such session")
		return
	}
	var in struct {
		Archive bool `json:"archive"`
	}
	_ = readJSON(r, &in)
	why := "fork"
	if in.Archive {
		why = "resume"
	}
	succ, err := a.Fork(s, in.Archive, why)
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	writeJSON(w, 201, a.enrich(succ))
}

func (a *App) hMarkRead(w http.ResponseWriter, r *http.Request) {
	s := a.store.Session(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "no such session")
		return
	}
	s.Unread = 0
	_ = a.store.PutSession(s)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	w.WriteHeader(204)
}

func (a *App) hSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		fail(w, 400, "q is required")
		return
	}
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	hits, err := a.store.Search(q, limit)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	writeJSON(w, 200, hits)
}

func (a *App) hJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := a.store.Jobs()
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	if sid := r.URL.Query().Get("session_id"); sid != "" {
		var out []*Job
		for _, j := range jobs {
			if j.SessionID == sid {
				out = append(out, j)
			}
		}
		jobs = out
	}
	writeJSON(w, 200, jobs)
}

func (a *App) hCreateJob(w http.ResponseWriter, r *http.Request) {
	var spec JobSpec
	if err := readJSON(r, &spec); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if spec.Check == "" && spec.ReasonNoCheck == "" {
		spec.ReasonNoCheck = "created from the interface"
	}
	j, err := a.CreateJob(spec)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	writeJSON(w, 201, j)
}

func (a *App) hJob(w http.ResponseWriter, r *http.Request) {
	j, err := a.store.Job(r.PathValue("id"))
	if err != nil || j == nil {
		fail(w, 404, "no such job")
		return
	}
	writeJSON(w, 200, j)
}

func (a *App) hPatchJob(w http.ResponseWriter, r *http.Request) {
	j, err := a.store.Job(r.PathValue("id"))
	if err != nil || j == nil {
		fail(w, 404, "no such job")
		return
	}
	var in struct {
		Schedule       *string `json:"schedule"`
		Check          *string `json:"check"`
		Prompt         *string `json:"prompt"`
		ExpiresAt      *string `json:"expires_at"`
		OnConditionMet *string `json:"on_condition_met"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if in.Schedule != nil {
		next, err := nextRun(*in.Schedule, time.Now())
		if err != nil {
			fail(w, 400, "%v", err)
			return
		}
		j.Schedule, j.NextRunAt = *in.Schedule, next
	}
	if in.Check != nil {
		j.Check = *in.Check
	}
	if in.Prompt != nil {
		j.Prompt = *in.Prompt
	}
	if in.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *in.ExpiresAt)
		if err != nil {
			fail(w, 400, "%v", err)
			return
		}
		j.ExpiresAt = t
	}
	if in.OnConditionMet != nil {
		j.OnConditionMet = *in.OnConditionMet
	}
	if err := a.store.PutJob(j); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	a.hub.Broadcast(wsEvent{Kind: "jobs"})
	writeJSON(w, 200, j)
}

func (a *App) hDeleteJob(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteJob(r.PathValue("id")); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	a.hub.Broadcast(wsEvent{Kind: "jobs"})
	w.WriteHeader(204)
}

func (a *App) hDeadLetters(w http.ResponseWriter, r *http.Request) {
	d, err := a.store.DeadLetters()
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, d)
}

func (a *App) hReplay(w http.ResponseWriter, r *http.Request) {
	j, err := a.ReplayDeadLetter(r.PathValue("id"))
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	writeJSON(w, 201, j)
}

func (a *App) hDismiss(w http.ResponseWriter, r *http.Request) {
	if err := a.store.CloseDeadLetter(r.PathValue("id")); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	a.hub.Broadcast(wsEvent{Kind: "dead_letters"})
	w.WriteHeader(204)
}

func (a *App) hMemory(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.Memory()
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "used": memoryUsage(items),
		"capacity": a.cfg.MemoryCapacity})
}

func (a *App) hAddMemory(w http.ResponseWriter, r *http.Request) {
	var in struct{ Text, SourceSession string }
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	m, err := a.AddMemory(in.Text, in.SourceSession)
	if err != nil {
		fail(w, 409, "%v", err)
		return
	}
	writeJSON(w, 201, m)
}

func (a *App) hPatchMemory(w http.ResponseWriter, r *http.Request) {
	var in struct{ Text string }
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	items, _ := a.store.Memory()
	for _, m := range items {
		if m.ID == r.PathValue("id") {
			m.Text = in.Text
			if err := a.store.PutMemory(m); err != nil {
				fail(w, 500, "%v", err)
				return
			}
			writeJSON(w, 200, m)
			return
		}
	}
	fail(w, 404, "no such item")
}

func (a *App) hDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteMemory(r.PathValue("id")); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	w.WriteHeader(204)
}

func (a *App) hTools(w http.ResponseWriter, r *http.Request) {
	sess := a.store.Session(r.URL.Query().Get("session_id"))
	tools := a.tools.All()
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		enabled := true
		if sess != nil {
			enabled = sess.toolEnabled(t.Name)
		}
		out = append(out, map[string]any{"name": t.Name, "description": t.Description,
			"db_prefix": t.DBPrefix, "timeout_seconds": t.Timeout, "parameters": t.Parameters,
			"has_panel": t.HasPanel, "builtin": t.Builtin, "loaded_at": t.LoadedAt,
			"enabled": enabled})
	}
	writeJSON(w, 200, map[string]any{"tools": out, "failures": a.tools.Failures()})
}

func (a *App) hReload(w http.ResponseWriter, r *http.Request) {
	loaded, failures := a.ReloadTools(r.URL.Query().Get("session_id"))
	writeJSON(w, 200, map[string]any{"loaded": loaded, "failures": failures})
}

func (a *App) hPanel(w http.ResponseWriter, r *http.Request) {
	t := a.tools.Get(r.PathValue("name"))
	if t == nil || !t.HasPanel {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(t.Dir, "ui", "panel.js"))
}

// hCallTool lets a tool panel read and write its own data through the API.
func (a *App) hCallTool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if a.tools.Get(name) == nil {
		fail(w, 404, "no such tool")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	res := a.tools.Call(r.Context(), &ToolCtx{App: a, SessionID: r.URL.Query().Get("session_id")}, name, body)
	writeJSON(w, 200, res)
}

func (a *App) hSkills(w http.ResponseWriter, r *http.Request) {
	sess := a.store.Session(r.URL.Query().Get("session_id"))
	skills := a.skills.All()
	out := make([]map[string]any, 0, len(skills))
	for _, s := range skills {
		enabled := true
		if sess != nil {
			enabled = sess.skillEnabled(s.Name)
		}
		out = append(out, map[string]any{"name": s.Name, "description": s.Description,
			"bytes": s.Bytes, "enabled": enabled})
	}
	writeJSON(w, 200, map[string]any{"skills": out, "failures": a.skills.Failures()})
}

func (a *App) hSkill(w http.ResponseWriter, r *http.Request) {
	s := a.skills.Get(r.PathValue("name"))
	if s == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"name": s.Name, "description": s.Description, "body": s.Body})
}

func (a *App) hUpload(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > maxUpload {
		fail(w, 413, "files larger than 100 MB are refused")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	sessionID := r.FormValue("session_id")
	if a.store.Session(sessionID) == nil {
		fail(w, 400, "session_id is required")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	defer file.Close()
	dir := filepath.Join(a.cfg.Workspace, "uploads", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	name := filepath.Base(hdr.Filename)
	dst := filepath.Join(dir, name)
	out, err := os.Create(dst)
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(file, maxUpload))
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	rel := filepath.Join("uploads", sessionID, name)
	a.appendEvent(sessionID, Entry{EventKind: "upload",
		Text: fmt.Sprintf("uploaded %s (%d bytes) to %s", name, n, rel)})
	writeJSON(w, 201, map[string]any{"path": rel, "bytes": n})
}

func (a *App) hPushKey(w http.ResponseWriter, r *http.Request) {
	pub, _ := a.vapidKeys()
	writeJSON(w, 200, map[string]string{"public_key": pub})
}

func (a *App) hPushSubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Endpoint string            `json:"endpoint"`
		Keys     map[string]string `json:"keys"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if err := a.store.PutPushSub(PushSub{Endpoint: in.Endpoint,
		P256dh: in.Keys["p256dh"], Auth: in.Keys["auth"]}); err != nil {
		fail(w, 500, "%v", err)
		return
	}
	w.WriteHeader(201)
}

func (a *App) hModels(w http.ResponseWriter, r *http.Request) {
	models, err := a.or.Models(r.Context())
	if err != nil {
		fail(w, 502, "%v", err)
		return
	}
	writeJSON(w, 200, models)
}

func (a *App) hStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"breaker":       a.sched.State(),
		"default_model": a.cfg.DefaultModel,
		"workspace":     a.cfg.Workspace,
	}
	if k, err := a.or.Key(r.Context()); err == nil {
		out["key"] = k
	}
	writeJSON(w, 200, out)
}
