// Package tool is what every tool binary is built on: the subprocess contract,
// access to the agent's own HTTP API, and the workspace boundary.
//
// A tool holds no private channel into the system. It reaches the agent over
// AGENT_URL, the same API the interface uses, and its filesystem reach is
// whatever the sandbox permits — which is its own session's working directory
// and nothing else.
package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The environment the registry hands a tool. Read once, so a tool reads the
// same values throughout a call.
var (
	BaseURL   = envOr("AGENT_URL", "http://127.0.0.1:8080")
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

var client = &http.Client{Timeout: 30 * time.Second}

// API calls the agent's HTTP API — the same one the interface uses. body is
// encoded as JSON when non-nil; out is filled from the response when non-nil.
// Any failure ends the call, because a tool that cannot reach the system has
// nothing further to say.
func API(method, path string, body any, query url.Values, out any) {
	u := BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			Failf("could not encode the request: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		Failf("could not build the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		Failf("cannot reach the agent API at %s: %v", BaseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		Failf("cannot read the reply from %s: %v", BaseURL, err)
	}
	if resp.StatusCode >= 400 {
		Failf("%s", APIError(raw, resp.Status))
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	if err := json.Unmarshal(raw, out); err != nil {
		Failf("could not parse the reply from %s: %v", BaseURL, err)
	}
}

// APIError reads the message out of an error reply, falling back to the status
// line. The API answers with {"error": "..."}; a proxy or a panic may not.
func APIError(raw []byte, status string) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	if s := strings.TrimSpace(string(raw)); s != "" {
		return s
	}
	return status
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
