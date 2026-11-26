package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A bundle is an MCP server packaged the way MCP Bundles package one: a folder
// holding manifest.json and the server it names (ADR-085). The agent asks the
// server what tools it has once, when the bundle is loaded, and starts it again
// for each call — through the sandbox, in the calling conversation's directory,
// ended when the call returns. That is how a tool's own program has always run
// here, so a bundle is confined exactly as a tool is and nothing long-lived is
// added for the agent to look after.

// bundleMetaKey is where a manifest keeps what MCPB has no field for and this
// agent needs, under _meta.
const bundleMetaKey = "io.github.jonaspf.agent"

// defaultBundleTimeout bounds a call to a bundle that names no timeout.
const defaultBundleTimeout = 30

// bundleManifest is the part of an MCPB manifest.json the agent reads.
type bundleManifest struct {
	ManifestVersion string `json:"manifest_version"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Description     string `json:"description"`
	Server          struct {
		Type      string `json:"type"`
		MCPConfig struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcp_config"`
	} `json:"server"`
	Compatibility struct {
		Platforms []string `json:"platforms"`
	} `json:"compatibility"`
	UserConfig map[string]struct {
		Type        string `json:"type"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Required    bool   `json:"required"`
		Sensitive   bool   `json:"sensitive"`
		Default     any    `json:"default"`
	} `json:"user_config"`
	Meta map[string]json.RawMessage `json:"_meta"`
}

// bundleMeta is what a manifest says under _meta for this agent: the same two
// things a tool's own manifest could ask for.
type bundleMeta struct {
	Reads   []string `json:"reads"`
	Timeout int      `json:"timeout_seconds"`
}

// bundle is one loaded bundle: where it is and how its server is started.
type bundle struct {
	Name, Version, Dir string
	Description        string
	command            string
	args, env          []string
	reads              []string
	timeout            int
	// config is what the manifest asks the operator for, and needs the keys
	// it cannot run without and has no value for.
	config []PluginSetting
	needs  []string
	// instructions is what the server says about using it, when it says
	// anything: it goes into the prompt of a conversation that has its tools.
	instructions string
}

func (b *bundle) label() string { return b.Name + " " + b.Version }

// readBundle reads the bundle unpacked at dir, filling what its manifest asks
// the operator for from what config holds under its name, and refuses one that could not run here,
// saying why. Whatever it could read of the bundle it answers either way, so
// that one that is refused can still be named.
func readBundle(dir string, config map[string]map[string]string) (*bundle, error) {
	abs := absOr(dir)
	b := &bundle{Name: filepath.Base(abs), Dir: abs, timeout: defaultBundleTimeout}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return b, fmt.Errorf("manifest.json: %w", err)
	}
	var m bundleManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return b, fmt.Errorf("manifest.json: %w", err)
	}
	if m.ManifestVersion == "" {
		return b, fmt.Errorf("manifest.json names no manifest_version, so it is not an MCP bundle")
	}
	if err := validAuthoredName(m.Name); err != nil {
		return b, fmt.Errorf("manifest.json: %w", err)
	}
	b.Name, b.Version, b.Description = m.Name, m.Version, m.Description
	if m.Version == "" {
		return b, fmt.Errorf("manifest.json names no version")
	}
	// One executable needs nothing from the image. A node or python server
	// needs a runtime inside the sandbox, and which one is a promise the image
	// does not make.
	if m.Server.Type != "binary" {
		return b, fmt.Errorf("server.type is %q; only a binary server runs here", m.Server.Type)
	}
	if p := m.Compatibility.Platforms; len(p) > 0 && !slices.Contains(p, "linux") {
		return b, fmt.Errorf("it runs on %s, and this is linux", strings.Join(p, ", "))
	}
	values := config[m.Name]
	// What the manifest asks the operator for: the value that was set, else
	// the manifest's own default, else nothing. A key it cannot run without
	// and has nothing for is named, and the bundle offers no tools until it
	// is set.
	filled := map[string]string{}
	for key, c := range m.UserConfig {
		v, set := values[key]
		b.config = append(b.config, PluginSetting{Key: key, Type: c.Type, Title: c.Title,
			Description: c.Description, Required: c.Required, Sensitive: c.Sensitive, Set: set})
		if !set {
			switch d := c.Default.(type) {
			case string:
				v = d
			case float64, bool:
				v = fmt.Sprint(d)
			}
			if c.Required && v == "" {
				b.needs = append(b.needs, key)
			}
		}
		filled["user_config."+key] = v
	}
	slices.SortFunc(b.config, func(x, y PluginSetting) int { return strings.Compare(x.Key, y.Key) })
	slices.Sort(b.needs)
	filled["__dirname"] = abs
	// fill puts the values in. What it has put in is not read again, so a
	// value that looks like a variable is only a value.
	fill := func(s string) (string, error) {
		var out strings.Builder
		for {
			i := strings.Index(s, "${")
			if i < 0 {
				return out.String() + s, nil
			}
			j := strings.Index(s[i:], "}")
			if j < 0 {
				return "", fmt.Errorf("mcp_config has an unclosed %s", s[i:])
			}
			v, ok := filled[s[i+2:i+j]]
			if !ok {
				return "", fmt.Errorf("mcp_config uses %s, which is not filled in here", s[i:i+j+1])
			}
			out.WriteString(s[:i] + v)
			s = s[i+j+1:]
		}
	}
	if b.command, err = fill(m.Server.MCPConfig.Command); err != nil {
		return b, err
	}
	if b.command == "" {
		return b, fmt.Errorf("server.mcp_config names no command")
	}
	if !filepath.IsAbs(b.command) {
		b.command = filepath.Join(abs, b.command)
	}
	// The server is the bundle's own: a command elsewhere would run whatever
	// is at that path with the bundle's name on it.
	if rel, err := filepath.Rel(abs, filepath.Clean(b.command)); err != nil || !filepath.IsLocal(rel) {
		return b, fmt.Errorf("its command is not inside the bundle")
	}
	if st, err := os.Stat(b.command); err != nil {
		return b, fmt.Errorf("its command: %w", err)
	} else if st.IsDir() || st.Mode()&0o111 == 0 {
		return b, fmt.Errorf("its command is not an executable file")
	}
	for _, arg := range m.Server.MCPConfig.Args {
		if arg, err = fill(arg); err != nil {
			return b, err
		}
		b.args = append(b.args, arg)
	}
	names := make([]string, 0, len(m.Server.MCPConfig.Env))
	for name := range m.Server.MCPConfig.Env {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		v, err := fill(m.Server.MCPConfig.Env[name])
		if err != nil {
			return b, err
		}
		// The model key is the agent's and no manifest is handed it; a name the
		// agent sets for every program is the agent's to set.
		if neverDeclared[name] || toolContractEnv[name] {
			continue
		}
		b.env = append(b.env, name+"="+v)
	}
	if raw, ok := m.Meta[bundleMetaKey]; ok {
		var meta bundleMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			return b, fmt.Errorf("_meta.%s: %w", bundleMetaKey, err)
		}
		b.reads = meta.Reads
		if meta.Timeout > 0 {
			b.timeout = meta.Timeout
		}
	}
	return b, nil
}

// serve starts the bundle's server confined to workspace and connects to it.
// The caller closes what it is given, which ends the server. What the server
// says of a call's progress goes to progress, when there is one.
func (b *bundle) serve(ctx context.Context, a *App, workspace string, reads, env []string,
	progress func(*mcp.ProgressNotificationParams)) (*mcp.ClientSession, *serverLog, func(), error) {
	argv := a.sandbox.Wrap(b.command, workspace, b.Dir, append(append([]string(nil), b.reads...), reads...), b.args)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = workspace
	cmd.Env = env
	// A group of its own, so that a server ended for running too long takes
	// with it whatever it had started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr := &serverLog{}
	cmd.Stderr = stderr
	// A question a server asks comes back as the call's result, for the agent
	// to put to the operator and to try the call again with the answer, maybe
	// days later; the SDK's own retry would hold the call open for it.
	opts := &mcp.ClientOptions{Capabilities: clientCapabilities(),
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true}}
	if progress != nil {
		opts.ProgressNotificationHandler = func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			progress(req.Params)
		}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: Version}, opts)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}, nil)
	if err != nil {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil, stderr, nil, err
	}
	// A call that was stopped has told its server so (the SDK sends
	// notifications/cancelled), and closing gives the server a moment to wind
	// down (TerminateDuration) before the group, with whatever it started, is
	// ended.
	stop := func() {
		_ = cs.Close()
		if ctx.Err() != nil && cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
	return cs, stderr, stop, nil
}

// tools asks the bundle's server what it offers. It runs in a directory of
// its own, made for the question and removed after it: listing is not any
// conversation's call, and no conversation's files are its to see.
func (b *bundle) tools(a *App) ([]*Tool, error) {
	scratch, err := os.MkdirTemp("", "bundle-list")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	// Saying what it has is not a call, and is not given a call's time.
	ctx, cancel := context.WithTimeout(context.Background(), defaultBundleTimeout*time.Second)
	defer cancel()
	env := append(toolBaseEnv(), b.env...)
	env = append(env, "TMPDIR="+scratch, "HOME="+scratch)
	cs, stderr, stop, err := b.serve(ctx, a, scratch, nil, env, nil)
	if err != nil {
		return nil, fmt.Errorf("its server did not start: %v%s", err, said(stderr))
	}
	defer stop()
	if ir := cs.InitializeResult(); ir != nil {
		b.instructions = strings.TrimSpace(ir.Instructions)
	}
	var out []*Tool
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("its server did not list its tools: %v%s", err, said(stderr))
		}
		raw, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", t.Name, err)
		}
		var params map[string]any
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, fmt.Errorf("tool %s: its input schema is not an object: %w", t.Name, err)
		}
		if typ, _ := params["type"].(string); typ != "object" {
			return nil, fmt.Errorf("tool %s: its input schema must be a JSON Schema object", t.Name)
		}
		if t.Name == "" || strings.TrimSpace(t.Description) == "" {
			return nil, fmt.Errorf("tool %q has no name or no description, and the description is all the model has to go on", t.Name)
		}
		output, err := outputSchemaOf(t.OutputSchema)
		if err != nil {
			return nil, fmt.Errorf("tool %s: its output schema: %w", t.Name, err)
		}
		title, hints := toolHints(t)
		out = append(out, &Tool{Name: t.Name, Description: t.Description, Parameters: params,
			Agent: ToolAgent{Timeout: b.timeout, Reads: b.reads}, Dir: b.Dir, bundle: b, app: appOf(t.Meta),
			output: output, title: title, hints: hints})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("its server lists no tools")
	}
	return out, nil
}

// toolHints is what a server says of a tool for the operator: its title, and
// the hints it gave outright. A hint left out is not shown as its default,
// which assumes the worst: a tool that says nothing is not called destructive.
func toolHints(t *mcp.Tool) (string, []string) {
	title := t.Title
	an := t.Annotations
	if an == nil {
		return title, nil
	}
	if title == "" {
		title = an.Title
	}
	var hints []string
	if an.ReadOnlyHint {
		hints = append(hints, "read-only")
	}
	if an.DestructiveHint != nil && *an.DestructiveHint && !an.ReadOnlyHint {
		hints = append(hints, "destructive")
	}
	if an.IdempotentHint {
		hints = append(hints, "idempotent")
	}
	if an.OpenWorldHint != nil && *an.OpenWorldHint {
		hints = append(hints, "open-world")
	}
	return title, hints
}

// progressLine is how far a call has got, as the screen says it: the tool,
// what its server said, and the count when it gave one.
func progressLine(tool string, p *mcp.ProgressNotificationParams) string {
	count := fmt.Sprintf("%g", p.Progress)
	if p.Total > 0 {
		count = fmt.Sprintf("%g of %g", p.Progress, p.Total)
	}
	if p.Message != "" {
		return fmt.Sprintf("%s: %s (%s)", tool, p.Message, count)
	}
	return fmt.Sprintf("%s: %s", tool, count)
}

// serverLog is what a server writes to standard error. The command copies
// into it while the server runs, and a call that ends early reads it before
// the server has stopped, so the two are kept apart.
type serverLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *serverLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *serverLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// said is what a server wrote to standard error, for a failure that would
// otherwise say only that there was one.
func said(stderr *serverLog) string {
	if s := strings.TrimSpace(stderr.String()); s != "" {
		return ": " + truncate(s, 300)
	}
	return ""
}

// serveIn starts a bundle's server for a conversation: confined to its
// directory, with what the conversation holds in its environment.
func (r *Registry) serveIn(ctx context.Context, tc *ToolCtx, b *bundle,
	progress func(*mcp.ProgressNotificationParams)) (*mcp.ClientSession, *serverLog, func(), error) {
	workspace := absOr(tc.App.ensureWorkspace(tc.SessionID))
	tmp := tc.App.sandbox.TempDir(workspace)
	_ = os.MkdirAll(tmp, 0o755)
	home := tc.App.sandbox.HomeDir(workspace)
	_ = os.MkdirAll(home, 0o755)
	// What a program needs to start, what the manifest sets, then what the
	// conversation holds, which takes the place of a manifest's variable of
	// the same name.
	env := append(toolBaseEnv(), b.env...)
	env = append(env, sessionVars(tc.vars())...)
	env = tc.App.withPackages(env, tc.SessionID)
	env = append(env, tc.App.skillDirsEnv(tc.SessionID), "TMPDIR="+tmp, "HOME="+home)
	return b.serve(ctx, tc.App, workspace, tc.App.skillFolders(tc.SessionID), env, progress)
}

// callBundle runs one call of a bundle's tool. Like any tool call it ends in a
// result the model can read, whatever went wrong.
func (r *Registry) callBundle(ctx context.Context, tc *ToolCtx, t *Tool, args json.RawMessage) toolResult {
	return r.callBundleWith(ctx, tc, t, args, nil, "")
}

// callBundleWith runs a call of a bundle's tool, handing its server the
// answers to what it asked before and what it said to hand back with them.
func (r *Registry) callBundleWith(ctx context.Context, tc *ToolCtx, t *Tool, args json.RawMessage,
	responses mcp.InputResponseMap, state string) toolResult {
	b := t.bundle
	cctx, cancel := context.WithTimeout(ctx, time.Duration(b.timeout)*time.Second)
	defer cancel()
	defer tc.App.workspaceChanged(tc.SessionID)

	fail := func(format string, a ...any) toolResult {
		if cctx.Err() == context.DeadlineExceeded {
			return errResult("tool %q exceeded its %ds timeout", t.Name, b.timeout)
		}
		return errResult(format, a...)
	}
	// How far a call has got is said on the conversation's screen while it
	// runs, the latest each time; it is not kept.
	progress := func(p *mcp.ProgressNotificationParams) {
		tc.App.hub.Broadcast(wsEvent{Kind: "progress", SessionID: tc.SessionID, Text: progressLine(t.Name, p)})
	}
	cs, stderr, stop, err := r.serveIn(cctx, tc, b, progress)
	if err != nil {
		res := fail("tool %q: its server did not start: %v", t.Name, err)
		res.stderr = stderr.String()
		return res
	}
	defer stop()
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	params := &mcp.CallToolParams{Name: t.Name, Arguments: args, InputResponses: responses, RequestState: state}
	params.SetProgressToken(fmt.Sprintf("%s-%d", t.Name, time.Now().UnixNano()))
	out, err := cs.CallTool(cctx, params)
	// A server too busy to answer asks for nothing and says to come back: it
	// is tried again, a little later each time, with what it said.
	for wait := busyWait; err == nil && out.NeedsInput() && len(out.InputRequests) == 0 && wait <= 4*busyWait; wait *= 2 {
		select {
		case <-cctx.Done():
		case <-time.After(wait):
		}
		params.InputResponses, params.RequestState = nil, out.RequestState
		out, err = cs.CallTool(cctx, params)
	}
	if err != nil {
		res := fail("tool %q failed: %v", t.Name, err)
		res.stderr = stderr.String()
		return res
	}
	if out.NeedsInput() {
		if len(out.InputRequests) == 0 {
			return toolResult{OK: false, Error: fmt.Sprintf("tool %q: its server stayed too busy to answer; try it again later", t.Name),
				stderr: stderr.String()}
		}
		q, err := questionOf(b, args, out)
		if err != nil {
			return toolResult{OK: false, Error: fmt.Sprintf("tool %q: %v", t.Name, err), stderr: stderr.String()}
		}
		return toolResult{OK: true, Content: q.toModel(t.Name), question: q, stderr: stderr.String()}
	}
	text := bundleText(out, saveIn(absOr(tc.App.ensureWorkspace(tc.SessionID)), t.Name))
	if note := t.output.check(t.Name, out.StructuredContent); note != "" {
		text += "\n" + note
	}
	res := toolResult{OK: !out.IsError, stderr: stderr.String()}
	res.app, _ = json.Marshal(out)
	if t.app.URI != "" {
		res.page = res.app
	}
	if out.IsError {
		res.Error = text
	} else {
		res.Content = text
	}
	return res
}

// bundleText is what a call answered, as the text the model is sent. A part
// that is not text is saved by save and named with where it went, so a model
// that cannot take it is not sent bytes, and one that can opens the file. A
// result that is only structured is sent as its JSON.
func bundleText(out *mcp.CallToolResult, save func(data []byte, mimeType, uri string) (string, error)) string {
	saved := func(what string, data []byte, mimeType, uri string) string {
		path, err := save(data, mimeType, uri)
		if err != nil {
			return fmt.Sprintf("[%s, %s, %s: could not be saved: %v]", what, mimeType, byteSize(len(data)), err)
		}
		return fmt.Sprintf("[%s, %s, %s, saved as %s]", what, mimeType, byteSize(len(data)), path)
	}
	var parts []string
	for _, c := range out.Content {
		switch c := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, c.Text)
		case *mcp.ImageContent:
			parts = append(parts, saved("a picture", c.Data, c.MIMEType, ""))
		case *mcp.AudioContent:
			parts = append(parts, saved("a sound", c.Data, c.MIMEType, ""))
		case *mcp.EmbeddedResource:
			r := c.Resource
			if r == nil {
				continue
			}
			if r.Blob != nil {
				parts = append(parts, saved("resource "+r.URI, r.Blob, r.MIMEType, r.URI))
			} else {
				parts = append(parts, fmt.Sprintf("[resource %s, %s]\n%s", r.URI, orUnknown(r.MIMEType), r.Text))
			}
		case *mcp.ResourceLink:
			line := fmt.Sprintf("[a link to resource %s (%s, %s)", c.URI, c.Name, orUnknown(c.MIMEType))
			if c.Description != "" {
				line += ": " + c.Description
			}
			parts = append(parts, line+"]")
		default:
			parts = append(parts, fmt.Sprintf("(a part that is not text was left out: %T)", c))
		}
	}
	if len(parts) == 0 && out.StructuredContent != nil {
		if raw, err := json.Marshal(out.StructuredContent); err == nil {
			return string(raw)
		}
	}
	return strings.Join(parts, "\n")
}

func orUnknown(mimeType string) string {
	if mimeType == "" {
		return "type not given"
	}
	return mimeType
}

// saveIn saves a part of a tool's result in the conversation's directory,
// under tool-output/, as <tool>-<n> with the first n not taken, and answers
// the path from the directory.
func saveIn(workspace, tool string) func(data []byte, mimeType, uri string) (string, error) {
	return func(data []byte, mimeType, uri string) (string, error) {
		dir := filepath.Join(workspace, toolOutputDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		ext := partExtension(mimeType, uri)
		for n := nextPart(dir, tool); ; n++ {
			name := fmt.Sprintf("%s-%d.%s", tool, n, ext)
			f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if os.IsExist(err) {
				continue
			}
			if err != nil {
				return "", err
			}
			_, werr := f.Write(data)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				return "", werr
			}
			return toolOutputDir + "/" + name, nil
		}
	}
}

// nextPart is the number after the highest a part of tool's results has in
// dir, whatever its extension, so parts are numbered in the order they came.
func nextPart(dir, tool string) int {
	entries, _ := os.ReadDir(dir)
	last := 0
	for _, e := range entries {
		stem := strings.TrimSuffix(e.Name(), path.Ext(e.Name()))
		if n, err := strconv.Atoi(strings.TrimPrefix(stem, tool+"-")); err == nil && strings.HasPrefix(stem, tool+"-") && n > last {
			last = n
		}
	}
	return last + 1
}

// toolOutputDir is where the parts of results that are not text are saved,
// in the conversation's directory.
const toolOutputDir = "tool-output"

// partExtensions name a saved part's file by its type; a type not here takes
// its address's extension, else bin.
var partExtensions = map[string]string{
	"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp", "image/svg+xml": "svg",
	"audio/wav": "wav", "audio/x-wav": "wav", "audio/wave": "wav", "audio/mpeg": "mp3", "audio/ogg": "ogg",
	"audio/webm": "webm", "audio/mp4": "m4a", "audio/flac": "flac",
	"application/pdf": "pdf", "application/json": "json", "text/plain": "txt", "text/csv": "csv",
	"text/html": "html", "text/markdown": "md",
}

func partExtension(mimeType, uri string) string {
	if ext, ok := partExtensions[strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))]; ok {
		return ext
	}
	if ext := strings.TrimPrefix(path.Ext(uri), "."); ext != "" && len(ext) <= 8 && isAlnum(ext) {
		return strings.ToLower(ext)
	}
	return "bin"
}

func isAlnum(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// byteSize says a part's size as a person would.
func byteSize(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d bytes", n)
	case n < 1_000_000:
		return fmt.Sprintf("%d KB", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
}

// examine reads the bundle at dir and asks its server what it offers: what
// the Plugins screen says of it, and the tools to offer if nothing is wrong.
func (a *App) examine(dir string, config map[string]map[string]string) (Plugin, []*Tool) {
	b, err := readBundle(dir, config)
	p := Plugin{Name: b.Name, Version: b.Version, Description: b.Description, Tools: []string{},
		Config: b.config, Needs: b.needs, Reads: b.reads, Timeout: b.timeout, dir: b.Dir}
	if p.Config == nil {
		p.Config = []PluginSetting{}
	}
	if p.Needs == nil {
		p.Needs = []string{}
	}
	if p.Reads == nil {
		p.Reads = []string{}
	}
	if err != nil {
		p.Failure = err.Error()
		return p, nil
	}
	tools, err := b.tools(a)
	if err != nil {
		p.Failure = err.Error()
		return p, nil
	}
	for _, t := range tools {
		p.Tools = append(p.Tools, t.Name)
	}
	return p, tools
}

// taken says why a bundle cannot join what is loaded, or nothing. The model
// can only be told of one tool by a name, and the operator of one bundle.
// replacing is an install: the installed bundle of the same name is the one
// being replaced, and is not in its way.
func (r *Registry) taken(p Plugin, tools []*Tool, replacing bool) string {
	for _, have := range r.plugins {
		if have.Name != p.Name {
			continue
		}
		if have.Source == sourceImage && replacing {
			return fmt.Sprintf("a bundle named %s ships with the agent, and is replaced with the agent", p.Name)
		}
		if !replacing {
			return fmt.Sprintf("there is already a bundle named %s", p.Name)
		}
	}
	for _, t := range tools {
		if have := r.tools[t.Name]; have != nil && (have.bundle == nil || have.bundle.Name != p.Name) {
			return fmt.Sprintf("there is already a tool named %s", t.Name)
		}
	}
	return ""
}

// loadBundles reads every bundle in the registry's bundle directories and
// registers its tools. The caller holds the registry's lock. A tool keeps the
// first bundle that offered it: a second of the same name is refused, since
// the model can only be told of one.
func (r *Registry) loadBundles(app *App) []string {
	var loaded []string
	r.plugins = nil
	if len(r.bundleDirs) == 0 {
		return nil
	}
	values := app.pluginValues()
	for _, root := range r.bundleDirs {
		entries, err := os.ReadDir(root)
		if err != nil {
			if !os.IsNotExist(err) {
				r.failures = append(r.failures, LoadFailure{Dir: root, Reason: err.Error()})
			}
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p, tools := app.examine(filepath.Join(root, e.Name()), values)
			p.Source = sourceImage
			if root == app.cfg.UserPluginsDir {
				p.Source = sourceInstalled
			}
			if p.Failure == "" {
				p.Failure = r.taken(p, tools, false)
			}
			reason := p.Failure
			if reason == "" && len(p.Needs) > 0 {
				reason = "it needs a value for " + strings.Join(p.Needs, ", ") + ", set on the Plugins screen"
			}
			r.plugins = append(r.plugins, p)
			if reason != "" {
				r.failures = append(r.failures, LoadFailure{Dir: e.Name(), Reason: reason})
				continue
			}
			for _, t := range tools {
				r.register(t)
				loaded = append(loaded, t.Name)
			}
		}
	}
	return loaded
}
