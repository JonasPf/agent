// Package tool is what every tool binary is built on: the subprocess contract
// and the workspace boundary.
//
// A tool holds no channel into the system at all: what the agent tells it, it
// tells it in the call's environment (ADR-086). Its filesystem reach is
// whatever the sandbox permits, which is its own session's working directory
// and nothing else.
package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The environment the registry hands a tool. Read once, so a tool reads the
// same values throughout a call.
var (
	Session   = os.Getenv("AGENT_SESSION")
	Job       = os.Getenv("AGENT_JOB")
	DBPath    = os.Getenv("AGENT_DB")
	Workspace = cwd() // the registry runs a tool with the workspace as its cwd
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

// Args decodes the call arguments from standard input into v, which should be a
// pointer to a struct describing this tool's parameters. Unparseable input is
// the registry's fault rather than the model's, and is reported as an error
// result like any other so the turn survives it.
func Args(v any) {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		Failf("could not read the call arguments: %v", err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}")
	}
	if err := json.Unmarshal(b, v); err != nil {
		Failf("could not parse the call arguments: %v", err)
	}
}

type result struct {
	OK      bool   `json:"ok"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// OK ends the call with content for the model, and exits.
func OK(content string) {
	emit(result{OK: true, Content: content})
}

// OKf is OK with a format string.
func OKf(format string, a ...any) { OK(fmt.Sprintf(format, a...)) }

// Failf ends the call with an error the model can read and act on, and exits.
// The process still exits 0: a tool that fails is a result, not a broken turn.
func Failf(format string, a ...any) {
	emit(result{OK: false, Error: fmt.Sprintf(format, a...)})
}

func emit(r result) {
	b, err := json.Marshal(r)
	if err != nil {
		// Nothing else can be done here, and silence would look like a crash.
		fmt.Printf(`{"ok":false,"error":%q}`+"\n", err.Error())
		os.Exit(0)
	}
	os.Stdout.Write(append(b, '\n'))
	os.Exit(0)
}

// InsideWorkspace resolves a path and refuses anything outside the working
// directory. The operating system refuses it too — this is the error message,
// not the boundary.
func InsideWorkspace(path string) string {
	full, err := ResolveInside(Workspace, path)
	if err != nil {
		Failf("%s", err)
	}
	return full
}

// ResolveInside is InsideWorkspace without the exit, so it can be tested.
func ResolveInside(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, full)
	}
	full = filepath.Clean(full)
	// A path that does not exist yet cannot be resolved through its symlinks,
	// so the deepest existing parent is resolved and the rest re-joined.
	if real, err := filepath.EvalSymlinks(full); err == nil {
		full = real
	} else if dir, err := filepath.EvalSymlinks(filepath.Dir(full)); err == nil {
		full = filepath.Join(dir, filepath.Base(full))
	}
	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace", path)
	}
	return full, nil
}
