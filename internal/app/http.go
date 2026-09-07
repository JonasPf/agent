package app

import (
	"bytes"
	"context"
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
	mux.HandleFunc("POST /sessions/{id}/rotate", a.hRotate)
	mux.HandleFunc("POST /sessions/{id}/read", a.hMarkRead)
	mux.HandleFunc("GET /sessions/{id}/files", a.hSessionFiles)
	mux.HandleFunc("POST /sessions/{id}/files", a.hUpload)
	mux.HandleFunc("GET /sessions/{id}/files/{path...}", a.hSessionFile)
	mux.HandleFunc("DELETE /sessions/{id}/files/{path...}", a.hDeleteSessionFile)
	mux.HandleFunc("GET /sessions/{id}/export", a.hExportSession)
	mux.HandleFunc("POST /sessions/import", a.hImportSession)
	mux.HandleFunc("GET /search", a.hSearch)

	mux.HandleFunc("GET /jobs", a.hJobs)
	mux.HandleFunc("POST /jobs", a.hCreateJob)
	mux.HandleFunc("GET /jobs/{id}", a.hJob)
	mux.HandleFunc("GET /jobs/{id}/runs", a.hJobRuns)
	mux.HandleFunc("PATCH /jobs/{id}", a.hPatchJob)
	mux.HandleFunc("DELETE /jobs/{id}", a.hDeleteJob)

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

	mux.HandleFunc("GET /models", a.hModels)
	mux.HandleFunc("GET /status", a.hStatus)
	mux.HandleFunc("/ws", a.handleWS)

	mux.Handle("/", revalidated(http.FileServer(http.Dir(a.cfg.WebDir))))
	return mux
}

// revalidated makes the browser ask before reusing an interface asset. The
// scripts have no version in their URLs, so a heuristically cached app.js runs
// against markup it no longer matches — which fails silently, looking like a bug
// in the page rather than a stale copy of it. no-cache still allows a 304, so
// an unchanged asset costs one conditional request.
func revalidated(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
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
	out.DiskBytes = a.sessionDiskBytes(s.ID)
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

// optionalSet is a set of names as it arrives over the wire, with three states
// a plain []string cannot hold apart: absent, meaning inherit; null, meaning
// every one of them; or a list, meaning exactly those.
type optionalSet struct {
	set     []string
	present bool
}

func (o *optionalSet) UnmarshalJSON(b []byte) error {
	o.present = true
	if string(b) == "null" {
		o.set = nil
		return nil
	}
	return json.Unmarshal(b, &o.set)
}

// configRequest is a session configuration as requested. Layering it over a base
// is what makes a rotation that changes one field keep the rest.
type configRequest struct {
	Model         string      `json:"model"`
	EnabledTools  optionalSet `json:"enabled_tools"`
	EnabledSkills optionalSet `json:"enabled_skills"`
}

func (c configRequest) applyTo(base SessionConfig) SessionConfig {
	if c.Model != "" {
		base.Model = c.Model
	}
	if c.EnabledTools.present {
		base.EnabledTools = c.EnabledTools.set
	}
	if c.EnabledSkills.present {
		base.EnabledSkills = c.EnabledSkills.set
	}
	return base
}

func (a *App) hCreateSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		configRequest
		From string `json:"from"`
	}
	_ = readJSON(r, &in)
	var seed *Session
	if in.From != "" {
		seed = a.store.Session(in.From)
	}
	from := ""
	if seed != nil {
		from = seed.ID
	}
	s, err := a.NewSession(in.applyTo(a.baseConfig(seed)), from)
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
		Title         *string     `json:"title"`
		Model         *string     `json:"model"`
		Status        *string     `json:"status"`
		Summary       *string     `json:"summary"`
		EnabledTools  optionalSet `json:"enabled_tools"`
		EnabledSkills optionalSet `json:"enabled_skills"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if in.Title != nil {
		s.Title = *in.Title
	}
	if in.Model != nil || in.EnabledTools.present || in.EnabledSkills.present {
		// A configuration is chosen before the session exists and fixed once it
		// does. There is one answer here, not two.
		fail(w, 409, "a session's model, tools, and skills are fixed for its life; "+
			"POST /sessions/%s/rotate to continue this conversation under a new configuration", s.ID)
		return
	}
	if in.Status != nil {
		s.Status = *in.Status
	}
	if in.Summary != nil {
		s.Summary = *in.Summary
	}
	_ = a.store.PutSession(s)
	a.hub.Broadcast(wsEvent{Kind: "sessions"})
	writeJSON(w, 200, a.enrich(s))
}

func (a *App) hDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := a.DeleteSession(r.PathValue("id")); err != nil {
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

// hRotate continues a conversation in a new session. Any configuration field
// left out keeps the predecessor's, so a plain rotation and a reconfiguration
// are the same request.
func (a *App) hRotate(w http.ResponseWriter, r *http.Request) {
	s := a.store.Session(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "no such session")
		return
	}
	var in struct {
		configRequest
		Archive bool `json:"archive"`
	}
	_ = readJSON(r, &in)
	why := "fork"
	if in.Archive {
		why = "rotation"
	}
	succ, err := a.Rotate(s, in.applyTo(s.SessionConfig), in.Archive, why)
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

// hJobRuns is a job's own history: every wake, what it did, and when. It is
// the answer to "has this been running?", which used to need reading the
// conversation the job fires into.
func (a *App) hJobRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := a.store.JobRuns(r.PathValue("id"))
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, runs)
}

// priceJobs attaches each job's estimate. The catalogue call is cached for a
// day and a failure only costs the money figures, so a job list never depends
// on reaching OpenRouter.
func (a *App) priceJobs(ctx context.Context, jobs []*Job) []*Job {
	prices := map[string]float64{}
	// No catalogue is not an error: the token figures stand on their own, and a
	// job list must never fail because a price was unavailable.
	if a.or != nil {
		if models, err := a.or.Models(ctx); err == nil {
			for _, m := range models {
				prices[m.ID] = m.PromptPrice
			}
		}
	}
	out := make([]*Job, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, a.priceJob(j, prices))
	}
	return out
}

func (a *App) priceJob(j *Job, prices map[string]float64) *Job {
	copied := *j
	tokens := 0
	price := 0.0
	if s := a.LiveSession(j.SessionID); s != nil {
		// What a wake actually sends is the system prompt plus the conversation.
		// Counting only the conversation would put a new session's cost at zero,
		// when its every turn already carries a few thousand tokens of prompt.
		tokens = projectedTokens(a.store.Entries(s.ID))
		if p, ok := a.promptEntry(s.ID); ok {
			tokens += sectionsTotal(p.Sections)
		}
		price = prices[s.Model]
	}
	est := estimateJob(j.Schedule, j.Check != "", tokens, price)
	copied.Estimate = &est
	return &copied
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
	writeJSON(w, 200, a.priceJobs(r.Context(), jobs))
}

func (a *App) hCreateJob(w http.ResponseWriter, r *http.Request) {
	var spec JobSpec
	if err := readJSON(r, &spec); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	j, err := a.CreateJob(spec)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	writeJSON(w, 201, a.priceJobs(r.Context(), []*Job{j})[0])
}

func (a *App) hJob(w http.ResponseWriter, r *http.Request) {
	j, err := a.store.Job(r.PathValue("id"))
	if err != nil || j == nil {
		fail(w, 404, "no such job")
		return
	}
	writeJSON(w, 200, a.priceJobs(r.Context(), []*Job{j})[0])
}

func (a *App) hPatchJob(w http.ResponseWriter, r *http.Request) {
	j, err := a.store.Job(r.PathValue("id"))
	if err != nil || j == nil {
		fail(w, 404, "no such job")
		return
	}
	var in struct {
		Schedule    *string `json:"schedule"`
		Check       *string `json:"check"`
		Prompt      *string `json:"prompt"`
		AfterActing *string `json:"after_acting"`
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
	if in.AfterActing != nil {
		if *in.AfterActing != afterStop && *in.AfterActing != afterContinue {
			fail(w, 400, "after_acting must be %q or %q", afterStop, afterContinue)
			return
		}
		j.AfterActing = *in.AfterActing
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

// hUpload writes a file into the session's working directory, where its tools
// run. An upload writes no transcript entry: the file is the record.
func (a *App) hUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.store.Session(id) == nil {
		fail(w, 404, "no such session")
		return
	}
	if r.ContentLength > maxUpload {
		fail(w, 413, "files larger than 100 MB are refused")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	defer file.Close()
	rel, n, err := a.writeWorkspaceFile(id, filepath.Base(hdr.Filename), file)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	writeJSON(w, 201, map[string]any{"path": rel, "bytes": n})
}

func (a *App) hSessionFiles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.store.Session(id) == nil {
		fail(w, 404, "no such session")
		return
	}
	files, err := a.sessionFiles(id)
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, files)
}

func (a *App) hSessionFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.store.Session(id) == nil {
		fail(w, 404, "no such session")
		return
	}
	full, err := a.resolveInWorkspace(id, r.PathValue("path"))
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	http.ServeFile(w, r, full)
}

func (a *App) hDeleteSessionFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.store.Session(id) == nil {
		fail(w, 404, "no such session")
		return
	}
	full, err := a.resolveInWorkspace(id, r.PathValue("path"))
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if err := os.Remove(full); err != nil {
		fail(w, 404, "%v", err)
		return
	}
	w.WriteHeader(204)
}

// hExportSession serves everything the session owns as one zip.
func (a *App) hExportSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := a.store.Session(id)
	if s == nil {
		fail(w, 404, "no such session")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "session-"+id+".zip"))
	if err := a.ExportSession(w, id); err != nil {
		// The archive has already begun; the truncated download is the signal.
		return
	}
}

// hImportSession restores a session from an archive, under the identifier the
// archive carries.
func (a *App) hImportSession(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > maxUpload {
		fail(w, 413, "archives larger than 100 MB are refused")
		return
	}
	var body io.Reader = io.LimitReader(r.Body, maxUpload)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			fail(w, 400, "%v", err)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			fail(w, 400, "%v", err)
			return
		}
		defer file.Close()
		body = file
	}
	blob, err := io.ReadAll(body)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	s, err := a.ImportSession(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		code := 400
		if strings.Contains(err.Error(), "already here") {
			code = 409
		}
		fail(w, code, "%v", err)
		return
	}
	writeJSON(w, 201, a.enrich(s))
}

func (a *App) hModels(w http.ResponseWriter, r *http.Request) {
	models, err := a.or.Models(r.Context())
	if err != nil {
		fail(w, 502, "%v", err)
		return
	}
	writeJSON(w, 200, filterModels(models, r.URL.Query().Get("q")))
}

func (a *App) hStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"breaker":       a.sched.State(),
		"sandbox":       a.sandbox,
		"default_model": a.cfg.DefaultModel,
		"workspace":     a.cfg.Workspace,
	}
	if k, err := a.or.Key(r.Context()); err == nil {
		out["key"] = k
	}
	writeJSON(w, 200, out)
}
