package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// A tool reaches the agent on an API of its own. It used to use the operator's,
// which can create a conversation holding a credential and then send it the
// message that spends it; a tool that could reach that could hand itself
// anything the operator had ever set. The tool API offers what the shipped
// tools call — memory, the calling conversation's jobs, the skills it has, and
// search — and the sandbox keeps a tool off the operator's port.
//
// Every request names the call it belongs to with a token issued for that call
// and revoked when it ends. The call, not the request, says which conversation
// it is, so a tool cannot act in a conversation it was not called from.

type callGrant struct{ SessionID, JobID string }

type callTokens struct {
	mu   sync.Mutex
	live map[string]callGrant
}

func (c *callTokens) issue(sessionID, jobID string) (string, func()) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	c.mu.Lock()
	if c.live == nil {
		c.live = map[string]callGrant{}
	}
	c.live[token] = callGrant{SessionID: sessionID, JobID: jobID}
	c.mu.Unlock()
	return token, func() {
		c.mu.Lock()
		delete(c.live, token)
		c.mu.Unlock()
	}
}

func (c *callTokens) lookup(token string) (callGrant, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g, ok := c.live[token]
	return g, ok
}

type callerKey struct{}

func caller(r *http.Request) callGrant {
	g, _ := r.Context().Value(callerKey{}).(callGrant)
	return g
}

// listenTools serves the tool API on a loopback port the kernel picks, and
// tells the sandbox, which lets tools connect to it. It runs before any tool
// can: a tool started without it has nowhere to reach the agent.
func (a *App) listenTools() (func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	a.toolAddr = ln.Addr().String()
	_, port, _ := net.SplitHostPort(a.toolAddr)
	n, _ := strconv.Atoi(port)
	a.sandbox.SetToolAPIPort(n)
	srv := &http.Server{Handler: a.toolRoutes()}
	go func() { _ = srv.Serve(ln) }()
	return func() { _ = srv.Close() }, nil
}

func (a *App) toolURL() string {
	if a.toolAddr == "" {
		return ""
	}
	return "http://" + a.toolAddr
}

func (a *App) toolRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /memory", a.hMemory)
	mux.HandleFunc("POST /memory", a.tAddMemory)
	mux.HandleFunc("PATCH /memory/{id}", a.hPatchMemory)
	mux.HandleFunc("DELETE /memory/{id}", a.hDeleteMemory)
	mux.HandleFunc("GET /jobs", a.tJobs)
	mux.HandleFunc("POST /jobs", a.tCreateJob)
	mux.HandleFunc("PATCH /jobs/{id}", a.ownJob(a.hPatchJob))
	mux.HandleFunc("DELETE /jobs/{id}", a.ownJob(a.hDeleteJob))
	mux.HandleFunc("GET /skills/{name}", a.tSkill)
	mux.HandleFunc("GET /search", a.hSearch)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		g, ok := a.calls.lookup(token)
		if token == "" || !ok {
			fail(w, 401, "the tool API answers a running tool call, and this request names none")
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, g)))
	})
}

// tAddMemory records who remembered something as the calling conversation,
// whatever the request says.
func (a *App) tAddMemory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	m, err := a.AddMemory(in.Text, caller(r).SessionID)
	if err != nil {
		fail(w, 409, "%v", err)
		return
	}
	writeJSON(w, 201, m)
}

func (a *App) tJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := a.store.SessionJobs(caller(r).SessionID)
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, a.priceJobs(r.Context(), jobs))
}

// tCreateJob schedules into the calling conversation. Naming another is
// refused rather than corrected: a job wakes its conversation with a prompt,
// and one that asked for someone else's should be told it did not get it.
func (a *App) tCreateJob(w http.ResponseWriter, r *http.Request) {
	var spec JobSpec
	if err := readJSON(r, &spec); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	mine := caller(r).SessionID
	if spec.SessionID != "" && spec.SessionID != mine {
		fail(w, 403, "a tool schedules into its own conversation only, not %s", spec.SessionID)
		return
	}
	spec.SessionID = mine
	j, err := a.CreateJob(spec)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	writeJSON(w, 201, a.priceJobs(r.Context(), []*Job{j})[0])
}

// ownJob lets a request through only for a job of the calling conversation.
// Another conversation's job is not there, as far as this caller can tell.
func (a *App) ownJob(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		j, err := a.store.Job(r.PathValue("id"))
		if err != nil || j == nil || j.SessionID != caller(r).SessionID {
			fail(w, 404, "no such job")
			return
		}
		h(w, r)
	}
}

// tSkill reads a skill the calling conversation has, and no other.
func (a *App) tSkill(w http.ResponseWriter, r *http.Request) {
	sess := a.store.Session(caller(r).SessionID)
	sk := a.skills.Get(r.PathValue("name"))
	if sess == nil || sk == nil || !sess.skillEnabled(sk) {
		fail(w, 404, "no such skill in this conversation")
		return
	}
	writeJSON(w, 200, map[string]any{"name": sk.Name, "description": sk.Description, "body": sk.Body})
}
