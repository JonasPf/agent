package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP Apps (ADR-090): a bundle's tool can name a page, and the operator opens
// it from that tool's result. The page is the bundle's, fetched from its
// server like anything else the server answers; it runs in a box in the
// interface with no network and no origin, and reaches the agent only through
// the two routes here: one that hands it the call that opened it, and one
// that calls a tool of its own bundle for it.

// appExtension is how a host says it takes MCP Apps, and appMime what a page
// is served as.
const (
	appExtension = "io.modelcontextprotocol/ui"
	appMime      = "text/html;profile=mcp-app"
)

// appCapabilities is what the agent says of itself to every bundle's server.
func appCapabilities() *mcp.ClientCapabilities {
	c := &mcp.ClientCapabilities{}
	c.AddExtension(appExtension, map[string]any{"mimeTypes": []string{appMime}})
	return c
}

// toolApp is what a tool's definition says under _meta.ui: the page its result
// opens, and who may call it. Unsaid, the model and the page both may.
type toolApp struct {
	URI      string
	forModel bool
	forPage  bool
}

func appOf(meta map[string]any) toolApp {
	app := toolApp{forModel: true, forPage: true}
	ui, _ := meta["ui"].(map[string]any)
	app.URI, _ = ui["resourceUri"].(string)
	if who, ok := ui["visibility"].([]any); ok {
		app.forModel, app.forPage = false, false
		for _, w := range who {
			switch w {
			case "model":
				app.forModel = true
			case "app":
				app.forPage = true
			}
		}
	}
	return app
}

// appEntry is the tool result at seq in a conversation, if it opened a page
// and the tool that made it is still here.
func (a *App) appEntry(sessionID string, seq int) (Entry, *Tool, bool) {
	for _, e := range a.store.Entries(sessionID) {
		if e.Seq != seq {
			continue
		}
		t := a.tools.Get(e.ToolName)
		if e.Role != "tool" || len(e.ToolApp) == 0 || t == nil || t.bundle == nil || t.app.URI == "" {
			break
		}
		return e, t, true
	}
	return Entry{}, nil, false
}

// hApp is what a page is opened with: the page itself, read from its bundle's
// server now, and the call that opened it — the tool, what it was called
// with, and its whole result.
func (a *App) hApp(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	seq, _ := strconv.Atoi(r.PathValue("seq"))
	e, t, ok := a.appEntry(sessionID, seq)
	if !ok {
		fail(w, 404, "no page was opened there, or the bundle that opened it is gone")
		return
	}
	html, err := a.readApp(r.Context(), sessionID, t)
	if err != nil {
		fail(w, 502, "%s: %v", t.bundle.label(), err)
		return
	}
	arguments := json.RawMessage("{}")
	for _, prior := range a.store.Entries(sessionID) {
		for _, c := range prior.ToolCalls {
			if c.ID == e.ToolCallID && json.Valid([]byte(c.Arguments)) {
				arguments = json.RawMessage(c.Arguments)
			}
		}
	}
	writeJSON(w, 200, map[string]any{
		"html":      html,
		"tool":      map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Parameters},
		"arguments": arguments,
		"result":    e.ToolApp,
		"bundle":    t.bundle.label(),
	})
}

// readApp reads a tool's page from its bundle's server, started confined in
// the conversation's directory as it is for a call.
func (a *App) readApp(ctx context.Context, sessionID string, t *Tool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultBundleTimeout*time.Second)
	defer cancel()
	cs, stderr, stop, err := a.tools.serveIn(ctx, &ToolCtx{App: a, SessionID: sessionID}, t.bundle)
	if err != nil {
		return "", fmt.Errorf("its server did not start: %v%s", err, said(stderr))
	}
	defer stop()
	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: t.app.URI})
	if err != nil {
		return "", fmt.Errorf("its server did not give %s: %v", t.app.URI, err)
	}
	for _, c := range res.Contents {
		if c.MIMEType != appMime {
			continue
		}
		if c.Text != "" {
			return c.Text, nil
		}
		if len(c.Blob) > 0 {
			return string(c.Blob), nil
		}
	}
	return "", fmt.Errorf("%s is not a page (%s)", t.app.URI, appMime)
}

// hAppCall calls a tool for a page: one its own bundle offers and allows a
// page to call, in the page's conversation. The call is written on the
// transcript, where the operator reads it and the model is not sent it.
func (a *App) hAppCall(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	seq, _ := strconv.Atoi(r.PathValue("seq"))
	_, opened, ok := a.appEntry(sessionID, seq)
	if !ok {
		fail(w, 404, "no page was opened there, or the bundle that opened it is gone")
		return
	}
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	t := a.tools.Get(in.Name)
	if t == nil || t.bundle == nil || t.bundle.Dir != opened.bundle.Dir || !t.app.forPage {
		fail(w, 403, "%s is not a tool this page may call", in.Name)
		return
	}
	res := a.tools.Call(r.Context(), &ToolCtx{App: a, SessionID: sessionID}, in.Name, in.Arguments)
	a.appendEvent(sessionID, Entry{EventKind: "app_call", ToolName: in.Name, Stderr: res.stderr,
		Text: fmt.Sprintf("the page of %s called %s", opened.Name, in.Name)})
	if res.app == nil {
		// It never reached the server: the page is told as a server would tell it.
		res.app, _ = json.Marshal(map[string]any{"isError": true,
			"content": []map[string]string{{"type": "text", "text": res.Error}}})
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(res.app)
}
