package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
// The caller closes what it is given, which ends the server.
func (b *bundle) serve(ctx context.Context, a *App, workspace string, reads, env []string) (*mcp.ClientSession, *strings.Builder, func(), error) {
	argv := a.sandbox.Wrap(b.command, workspace, b.Dir, append(append([]string(nil), b.reads...), reads...), b.args)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = workspace
	cmd.Env = env
	// A group of its own, so that a server ended for running too long takes
	// with it whatever it had started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: Version}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}, nil)
	if err != nil {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil, stderr, nil, err
	}
	stop := func() {
		if ctx.Err() != nil && cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		_ = cs.Close()
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
	cs, stderr, stop, err := b.serve(ctx, a, scratch, nil, env)
	if err != nil {
		return nil, fmt.Errorf("its server did not start: %v%s", err, said(stderr))
	}
	defer stop()
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
		out = append(out, &Tool{Name: t.Name, Description: t.Description, Parameters: params,
			Agent: ToolAgent{Timeout: b.timeout, Reads: b.reads}, Dir: b.Dir, bundle: b})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("its server lists no tools")
	}
	return out, nil
}

// said is what a server wrote to standard error, for a failure that would
// otherwise say only that there was one.
func said(stderr *strings.Builder) string {
	if s := strings.TrimSpace(stderr.String()); s != "" {
		return ": " + truncate(s, 300)
	}
	return ""
}

// callBundle runs one call of a bundle's tool. Like any tool call it ends in a
// result the model can read, whatever went wrong.
func (r *Registry) callBundle(ctx context.Context, tc *ToolCtx, t *Tool, args json.RawMessage) toolResult {
	b := t.bundle
	cctx, cancel := context.WithTimeout(ctx, time.Duration(b.timeout)*time.Second)
	defer cancel()
	workspace := absOr(tc.App.ensureWorkspace(tc.SessionID))
	defer tc.App.workspaceChanged(tc.SessionID)
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

	fail := func(format string, a ...any) toolResult {
		if cctx.Err() == context.DeadlineExceeded {
			return errResult("tool %q exceeded its %ds timeout", t.Name, b.timeout)
		}
		return errResult(format, a...)
	}
	cs, stderr, stop, err := b.serve(cctx, tc.App, workspace, tc.App.skillFolders(tc.SessionID), env)
	if err != nil {
		res := fail("tool %q: its server did not start: %v", t.Name, err)
		res.stderr = stderr.String()
		return res
	}
	defer stop()
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	out, err := cs.CallTool(cctx, &mcp.CallToolParams{Name: t.Name, Arguments: args})
	if err != nil {
		res := fail("tool %q failed: %v", t.Name, err)
		res.stderr = stderr.String()
		return res
	}
	text := bundleText(out)
	res := toolResult{OK: !out.IsError, stderr: stderr.String()}
	if out.IsError {
		res.Error = text
	} else {
		res.Content = text
	}
	return res
}

// bundleText is what a call answered, as the text the model is sent. A result
// that is only structured is sent as its JSON; a part that is not text is
// named rather than dropped without a word.
func bundleText(out *mcp.CallToolResult) string {
	var parts []string
	for _, c := range out.Content {
		switch c := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, c.Text)
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
