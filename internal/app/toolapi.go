package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// A tool reaches the agent on an API of its own, never the operator's, which
// can create a conversation holding a credential and then send it the message
// that spends it. What is left of that API is one question: where a skill the
// calling conversation has keeps its files, which the read tool asks. Jobs,
// search, other models, and packages are the agent's own tools now and run
// inside it (ADR-086); this goes too once read no longer asks.
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
	mux.HandleFunc("GET /skills/{name}", a.tSkill)
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

// tSkill reads a skill the calling conversation has, and no other: its body,
// and the paths of the files beside it, which are read one at a time.
func (a *App) tSkill(w http.ResponseWriter, r *http.Request) {
	sk := a.callerSkill(w, r)
	if sk == nil {
		return
	}
	files := make([]map[string]any, 0, len(sk.Files))
	for _, f := range sk.Files {
		files = append(files, map[string]any{"path": f.Path, "bytes": f.Bytes})
	}
	// The source is the file as stored, front matter and all, so a skill is an
	// example of the format and a revision starts from the whole of it. A
	// folder says where it is: its scripts run from there (ADR-071).
	dir := ""
	if sk.Main() == skillMain {
		dir = absOr(filepath.Dir(sk.Path))
	}
	writeJSON(w, 200, map[string]any{"name": sk.Name, "description": sk.Description, "body": sk.Body,
		"source": sk.Source, "dir": dir, "files": files})
}

func (a *App) callerSkill(w http.ResponseWriter, r *http.Request) *Skill {
	sess := a.store.Session(caller(r).SessionID)
	sk := a.skills.Get(r.PathValue("name"))
	if sess == nil || sk == nil || !a.hasSkill(sess, sk) {
		fail(w, 404, "no such skill in this conversation")
		return nil
	}
	return sk
}
