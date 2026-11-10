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
		Required bool `json:"required"`
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
	command            string
	args, env          []string
	reads              []string
	timeout            int
}

func (b *bundle) label() string { return b.Name + " " + b.Version }

// readBundle reads the bundle unpacked at dir and refuses one that could not
// run here, saying why.
func readBundle(dir string) (*bundle, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	var m bundleManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if m.ManifestVersion == "" {
		return nil, fmt.Errorf("manifest.json names no manifest_version, so it is not an MCP bundle")
	}
	if err := validAuthoredName(m.Name); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if m.Version == "" {
		return nil, fmt.Errorf("manifest.json names no version")
	}
	// One executable needs nothing from the image. A node or python server
	// needs a runtime inside the sandbox, and which one is a promise the image
	// does not make.
	if m.Server.Type != "binary" {
		return nil, fmt.Errorf("server.type is %q; only a binary server runs here", m.Server.Type)
	}
	if p := m.Compatibility.Platforms; len(p) > 0 && !slices.Contains(p, "linux") {
		return nil, fmt.Errorf("it runs on %s, and this is linux", strings.Join(p, ", "))
	}
	// What a bundle asks the operator for is asked on the screen that installs
	// it. Until there is one, a bundle that cannot run without an answer is
	// refused rather than started without it.
	for key, c := range m.UserConfig {
		if c.Required {
			return nil, fmt.Errorf("it needs a value for %q, and bundles cannot be configured yet", key)
		}
	}
	abs := absOr(dir)
	fill := func(s string) (string, error) {
		s = strings.ReplaceAll(s, "${__dirname}", abs)
		if i := strings.Index(s, "${"); i >= 0 {
			return "", fmt.Errorf("mcp_config uses %s, which is not filled in here", s[i:])
		}
		return s, nil
	}
	b := &bundle{Name: m.Name, Version: m.Version, Dir: abs, timeout: defaultBundleTimeout}
	if b.command, err = fill(m.Server.MCPConfig.Command); err != nil {
		return nil, err
	}
	if b.command == "" {
		return nil, fmt.Errorf("server.mcp_config names no command")
	}
	if !filepath.IsAbs(b.command) {
		b.command = filepath.Join(abs, b.command)
	}
	// The server is the bundle's own: a command elsewhere would run whatever
	// is at that path with the bundle's name on it.
	if rel, err := filepath.Rel(abs, filepath.Clean(b.command)); err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("its command is not inside the bundle")
	}
	if st, err := os.Stat(b.command); err != nil {
		return nil, fmt.Errorf("its command: %w", err)
	} else if st.IsDir() || st.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("its command is not an executable file")
	}
	for _, arg := range m.Server.MCPConfig.Args {
		if arg, err = fill(arg); err != nil {
			return nil, err
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
			return nil, err
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
			return nil, fmt.Errorf("_meta.%s: %w", bundleMetaKey, err)
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(b.timeout)*time.Second)
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
	env = append(env, "TMPDIR="+tmp, "HOME="+home)

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

// loadBundles reads every bundle in the registry's bundle directories and
// registers its tools. The caller holds the registry's lock. A tool keeps the
// first bundle that offered it: a second of the same name is refused, since
// the model can only be told of one.
func (r *Registry) loadBundles(app *App) []string {
	var loaded []string
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
			refuse := func(err error) {
				r.failures = append(r.failures, LoadFailure{Dir: e.Name(), Reason: err.Error()})
			}
			b, err := readBundle(filepath.Join(root, e.Name()))
			if err != nil {
				refuse(err)
				continue
			}
			tools, err := b.tools(app)
			if err != nil {
				refuse(err)
				continue
			}
			taken := ""
			for _, t := range tools {
				if have := r.tools[t.Name]; have != nil && (have.bundle == nil || have.bundle.Dir != b.Dir) {
					taken = t.Name
				}
			}
			if taken != "" {
				refuse(fmt.Errorf("there is already a tool named %s", taken))
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
