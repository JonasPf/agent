package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func (a *App) registerBuiltins() {
	b := func(name, desc string, params map[string]any, fn builtinFn) {
		a.tools.mu.Lock()
		a.tools.register(&Tool{Name: name, Description: desc, Parameters: params,
			Timeout: 60, Builtin: true, run: fn})
		a.tools.mu.Unlock()
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

	b("bash", "Run a shell command in the workspace.",
		obj(map[string]any{"command": str("Shell command."),
			"timeout_seconds": map[string]any{"type": "integer"}}, "command"), a.bashTool)

	b("read", "Read a file from the workspace.",
		obj(map[string]any{"path": str("Path, relative to the workspace."),
			"offset": map[string]any{"type": "integer"}, "limit": map[string]any{"type": "integer"}}, "path"),
		a.readTool)

	b("write", "Write a file in the workspace, creating or replacing it.",
		obj(map[string]any{"path": str("Path."), "content": str("Full file content.")}, "path", "content"),
		a.writeTool)

	b("edit", "Replace an exact string in a workspace file.",
		obj(map[string]any{"path": str("Path."), "old": str("Exact text to replace."),
			"new": str("Replacement text."), "replace_all": map[string]any{"type": "boolean"}},
			"path", "old", "new"), a.editTool)

	b("web_fetch", "Fetch a URL and return its text.",
		obj(map[string]any{"url": str("Absolute URL.")}, "url"), a.webFetchTool)

	b("web_search", "Search the web and return result titles, URLs, and snippets.",
		obj(map[string]any{"query": str("Search query.")}, "query"), a.webSearchTool)

	b("schedule", "Create, list, edit, or delete jobs attached to a session.",
		obj(map[string]any{
			"action":           map[string]any{"type": "string", "enum": []string{"create", "list", "edit", "delete"}},
			"id":               str("Job id, for edit and delete."),
			"schedule":         str("An interval (2m), a cron expression (0 9 * * *), or an RFC 3339 instant."),
			"check":            str("Shell command whose exit status zero means the condition is met. Prefer this."),
			"prompt":           str("What to ask the agent when the job runs."),
			"expires_at":       str("RFC 3339. Defaults to 24 hours out."),
			"on_condition_met": map[string]any{"type": "string", "enum": []string{"delete", "continue"}},
			"reason_no_check":  str("Required when creating a job without a check: why no command could decide the condition."),
		}, "action"), a.scheduleTool)

	b("memory", "Write, revise, or remove durable memory.",
		obj(map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"add", "edit", "delete", "list"}},
			"id":     str("Item id, for edit and delete."),
			"text":   str("The item, one fact."),
		}, "action"), a.memoryTool)

	b("session_search", "Full-text search across every transcript, including archived sessions.",
		obj(map[string]any{"query": str("FTS5 query."), "limit": map[string]any{"type": "integer"}}, "query"),
		a.searchTool)

	b("skill_read", "Load the full text of a skill.",
		obj(map[string]any{"name": str("Skill name.")}, "name"), a.skillReadTool)

	b("reload_tools", "Validate and register tools from disk.", obj(map[string]any{}), a.reloadTool)

	b("notify", "Send an interrupting notification. For a job without a check, calling this is what marks the condition met.",
		obj(map[string]any{"title": str("Short title."), "body": str("What happened.")}, "title", "body"),
		a.notifyTool)
}

func decode(args json.RawMessage, v any) error {
	if len(args) == 0 {
		return nil
	}
	return json.Unmarshal(args, v)
}

// workspace path resolution keeps tools inside the workspace directory.
func (a *App) resolve(p string) (string, error) {
	root, err := filepath.Abs(a.cfg.Workspace)
	if err != nil {
		return "", err
	}
	full := p
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, p)
	}
	full = filepath.Clean(full)
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q is outside the workspace", p)
	}
	return full, nil
}

func (a *App) bashTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if in.Timeout <= 0 {
		in.Timeout = 120
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(in.Timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", in.Command)
	cmd.Dir = a.cfg.Workspace
	out, err := cmd.CombinedOutput()
	body := truncate(string(out), 20000)
	if cctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("timed out after %ds\n%s", in.Timeout, body)
	}
	if err != nil {
		return nil, fmt.Errorf("exit: %v\n%s", err, body)
	}
	if body == "" {
		body = "(no output)"
	}
	return body, nil
}

func (a *App) readTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	p, err := a.resolve(in.Path)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(b), "\n")
	if in.Offset > 0 && in.Offset < len(lines) {
		lines = lines[in.Offset:]
	}
	if in.Limit > 0 && in.Limit < len(lines) {
		lines = lines[:in.Limit]
	}
	return strings.Join(lines, "\n"), nil
}

func (a *App) writeTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct{ Path, Content string }
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	p, err := a.resolve(in.Path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(in.Content), 0o644); err != nil {
		return nil, err
	}
	return fmt.Sprintf("wrote %s (%d bytes)", in.Path, len(in.Content)), nil
}

func (a *App) editTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Path       string `json:"path"`
		Old        string `json:"old"`
		New        string `json:"new"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	p, err := a.resolve(in.Path)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	s := string(b)
	n := strings.Count(s, in.Old)
	if n == 0 {
		return nil, fmt.Errorf("old string not found in %s", in.Path)
	}
	if n > 1 && !in.ReplaceAll {
		return nil, fmt.Errorf("old string appears %d times in %s; pass replace_all or give more context", n, in.Path)
	}
	if in.ReplaceAll {
		s = strings.ReplaceAll(s, in.Old, in.New)
	} else {
		s = strings.Replace(s, in.Old, in.New, 1)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		return nil, err
	}
	return fmt.Sprintf("edited %s (%d replacement(s))", in.Path, n), nil
}

var tagRE = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]+>`)
var wsRE = regexp.MustCompile(`\n{3,}`)

func fetchText(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; agent/0.1)")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	body := string(b)
	if strings.Contains(resp.Header.Get("Content-Type"), "html") {
		body = tagRE.ReplaceAllString(body, " ")
		body = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ").Replace(body)
		var lines []string
		for _, l := range strings.Split(body, "\n") {
			if t := strings.TrimSpace(l); t != "" {
				lines = append(lines, t)
			}
		}
		body = strings.Join(lines, "\n")
	}
	return wsRE.ReplaceAllString(body, "\n\n"), nil
}

func (a *App) webFetchTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct{ URL string }
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	body, err := fetchText(ctx, in.URL)
	if err != nil {
		return nil, err
	}
	return truncate(body, 40000), nil
}

func (a *App) webSearchTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct{ Query string }
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	body, err := fetchText(ctx, "https://html.duckduckgo.com/html/?q="+url.QueryEscape(in.Query))
	if err != nil {
		return nil, err
	}
	return truncate(body, 12000), nil
}

func (a *App) searchTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 {
		in.Limit = 20
	}
	hits, err := a.store.Search(in.Query, in.Limit)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return "no matches", nil
	}
	var sb strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&sb, "%s #%d (%s): %s\n", h.SessionID, h.Seq, h.Title, h.Snippet)
	}
	return sb.String(), nil
}

func (a *App) skillReadTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct{ Name string }
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	sess := a.store.Session(tc.SessionID)
	if sess != nil && !sess.skillEnabled(in.Name) {
		return nil, fmt.Errorf("skill %q is disabled in session %s", in.Name, tc.SessionID)
	}
	sk := a.skills.Get(in.Name)
	if sk == nil {
		return nil, fmt.Errorf("no skill named %q", in.Name)
	}
	return sk.Body, nil
}

func (a *App) reloadTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	loaded, failures := a.ReloadTools(tc.SessionID)
	var sb strings.Builder
	fmt.Fprintf(&sb, "loaded: %s\n", strings.Join(loaded, ", "))
	for _, f := range failures {
		fmt.Fprintf(&sb, "failed %s: %s\n", f.Dir, f.Reason)
	}
	return sb.String(), nil
}

func (a *App) notifyTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct{ Title, Body string }
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	if tc.Fired != nil {
		*tc.Fired = true
	}
	a.Notify(Notification{Title: in.Title, Body: in.Body, SessionID: tc.SessionID, JobID: tc.JobID})
	return "notification sent", nil
}
