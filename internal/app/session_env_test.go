package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A conversation holds its own variables (ADR-068): names and values the
// operator sets when it starts or forks, and changes from its details at any
// time. Its tools and checks receive them, nothing else does, and no route says
// what any of them holds.

// installEnvDump writes a tool that answers with the whole environment it was
// started with, base64 so that no value needs quoting.
func installEnvDump(t *testing.T, a *App) {
	t.Helper()
	dir := filepath.Join(a.tools.dir, "envdump")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"envdump","description":"Dump the environment.",
	  "parameters":{"type":"object","properties":{}},"agent":{"db_prefix":"envdump_"}}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat >/dev/null\nprintf '{\"ok\":true,\"content\":\"%s\"}' \"$(env | base64 -w0)\"\n"
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("envdump did not load: %+v", failures)
	}
}

// envIn is the environment a tool call in the session sees, run the way a
// turn runs it: as a subprocess, through the registry.
func envIn(t *testing.T, a *App, sessionID string) map[string]string {
	t.Helper()
	res := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: sessionID}, "envdump", json.RawMessage(`{}`))
	if !res.OK {
		t.Fatalf("envdump: %s (stderr %s)", res.Error, res.stderr)
	}
	raw, err := base64.StdEncoding.DecodeString(res.Content)
	if err != nil {
		t.Fatalf("envdump answered %q: %v", res.Content, err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return out
}

func start(t *testing.T, a *App, body string) Session {
	t.Helper()
	return decodeSession(t, call(t, a, "POST", "/sessions", body))
}

func TestAConversationsVariablesReachItsToolsAndNoOtherConversation(t *testing.T) {
	a := newTestApp(t)
	installEnvDump(t, a)
	mine := start(t, a, `{"model":"test/model","env":{"DEPLOY_TOKEN":"tok-1","REGION":"eu"}}`)
	if !reflect.DeepEqual(mine.Env, []string{"DEPLOY_TOKEN", "REGION"}) {
		t.Errorf("the session says it holds %v", mine.Env)
	}
	if got := envIn(t, a, mine.ID); got["DEPLOY_TOKEN"] != "tok-1" || got["REGION"] != "eu" {
		t.Errorf("a tool in the conversation saw DEPLOY_TOKEN=%q REGION=%q", got["DEPLOY_TOKEN"], got["REGION"])
	}
	// A new conversation takes the most recent one's tools and skills, and
	// none of its variables; nor does one started from it by name.
	for _, body := range []string{`{"model":"test/model"}`, `{"from":"` + mine.ID + `"}`} {
		other := start(t, a, body)
		if len(other.Env) != 0 {
			t.Errorf("%s: a new conversation holds %v", body, other.Env)
		}
		if _, ok := envIn(t, a, other.ID)["DEPLOY_TOKEN"]; ok {
			t.Errorf("%s: another conversation's variable reached this one's tool", body)
		}
	}
}

func TestAVariableChangesMidConversationAndTheConversationSaysSo(t *testing.T) {
	a := newTestApp(t)
	installEnvDump(t, a)
	s := start(t, a, `{"model":"test/model","env":{"ALPHA":"1","BETA":"2"}}`)
	w := patchSession(t, a, s.ID, `{"env":{"ALPHA":"one","BETA":null,"GAMMA":"3"}}`)
	got := decodeSession(t, w)
	if !reflect.DeepEqual(got.Env, []string{"ALPHA", "GAMMA"}) {
		t.Errorf("after the change the session holds %v", got.Env)
	}
	env := envIn(t, a, s.ID)
	if env["ALPHA"] != "one" || env["GAMMA"] != "3" {
		t.Errorf("the next call saw ALPHA=%q GAMMA=%q", env["ALPHA"], env["GAMMA"])
	}
	if _, ok := env["BETA"]; ok {
		t.Error("a removed variable still reached the tool")
	}
	entries := a.store.Entries(s.ID)
	last := entries[len(entries)-1]
	if last.Type != "event" || last.EventKind != "env" {
		t.Fatalf("the change was not written into the conversation; last entry is %+v", last)
	}
	for _, name := range []string{"ALPHA", "BETA", "GAMMA"} {
		if !strings.Contains(last.Text, name) {
			t.Errorf("the event %q does not name %s", last.Text, name)
		}
	}
	for _, value := range []string{"one", "3"} {
		if strings.Contains(last.Text, value) {
			t.Errorf("the event %q says what a variable holds", last.Text)
		}
	}
}

// What is set can be read by name; what it holds goes only to the
// conversation's own tools and checks.
func TestNoRouteSaysWhatAVariableHolds(t *testing.T) {
	const secret = "tok-should-never-be-shown-7f3a"
	a := newTestApp(t)
	s := start(t, a, `{"model":"test/model","env":{"DEPLOY_TOKEN":"`+secret+`"}}`)
	patchSession(t, a, s.ID, `{"env":{"OTHER_TOKEN":"`+secret+`-2"}}`)

	for _, path := range []string{"/sessions", "/sessions/" + s.ID, "/sessions/" + s.ID + "/transcript"} {
		w := call(t, a, "GET", path, "")
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("GET %s answers with a variable's value", path)
		}
		if !strings.Contains(w.Body.String(), "OTHER_TOKEN") {
			t.Errorf("GET %s does not say which variables the conversation holds", path)
		}
	}

	archive := exportSessionZip(t, a, s.ID)
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Contains(string(body), secret) {
			t.Errorf("the export's %s carries a variable's value", f.Name)
		}
	}

	// Kept where no tool of any conversation reaches: not in a working
	// directory or beside the database every tool is handed, and readable by
	// the agent alone.
	for _, reach := range []string{a.sessionWorkspace(s.ID), filepath.Dir(DBPath(a.cfg.DataDir))} {
		_ = filepath.WalkDir(reach, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if b, _ := os.ReadFile(path); strings.Contains(string(b), secret) {
				t.Errorf("%s holds a variable's value, and a tool can read it", path)
			}
			return nil
		})
	}
	held := filepath.Join(a.cfg.DataDir, "sessions", s.ID, "env.json")
	info, err := os.Stat(held)
	if err != nil {
		t.Fatalf("the values are not where the session keeps them: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("%s is %v; only the agent should read it", held, info.Mode().Perm())
	}
}

func TestAForkCarriesItsOriginsVariablesAndCanChangeThem(t *testing.T) {
	a := newTestApp(t)
	installEnvDump(t, a)
	origin := start(t, a, `{"model":"test/model","env":{"ALPHA":"1","BETA":"2"}}`)
	fork := decodeSession(t, call(t, a, "POST", "/sessions/"+origin.ID+"/fork", `{"env":{"BETA":null,"GAMMA":"3"}}`))
	if !reflect.DeepEqual(fork.Env, []string{"ALPHA", "GAMMA"}) {
		t.Errorf("the fork holds %v", fork.Env)
	}
	if env := envIn(t, a, fork.ID); env["ALPHA"] != "1" || env["GAMMA"] != "3" || env["BETA"] != "" {
		t.Errorf("the fork's tool saw %v", env)
	}
	patchSession(t, a, fork.ID, `{"env":{"ALPHA":"changed"}}`)
	if env := envIn(t, a, origin.ID); env["ALPHA"] != "1" || env["BETA"] != "2" {
		t.Errorf("changing the fork changed its origin: %v", env)
	}
	if got := a.store.Session(origin.ID).Env; !reflect.DeepEqual(got, []string{"ALPHA", "BETA"}) {
		t.Errorf("the origin holds %v", got)
	}
}

// A name the agent sets on every call would be overwritten, or would break the
// call, so it is refused by name rather than quietly dropped; and a refused
// request changes nothing, wherever it was sent.
func TestANameTheAgentSetsOrThatIsNoNameIsRefused(t *testing.T) {
	a := newTestApp(t)
	s := start(t, a, `{"model":"test/model","env":{"KEEP":"1"}}`)
	for _, name := range []string{"AGENT_URL", "AGENT_TOKEN", "HOME", "TMPDIR", "PATH", "1ABC", "A-B", "A B"} {
		body, _ := json.Marshal(map[string]any{"env": map[string]string{name: "x", "FINE": "y"}})
		before := len(a.store.Sessions())
		for _, r := range []struct{ method, path string }{
			{"POST", "/sessions"}, {"POST", "/sessions/" + s.ID + "/fork"}, {"PATCH", "/sessions/" + s.ID},
		} {
			w := call(t, a, r.method, r.path, string(body))
			if w.Code != 400 {
				t.Errorf("%s %s with %q = %d, want 400", r.method, r.path, name, w.Code)
				continue
			}
			if !strings.Contains(w.Body.String(), name) {
				t.Errorf("%s %s refused %q without naming it: %s", r.method, r.path, name, w.Body)
			}
		}
		if n := len(a.store.Sessions()); n != before {
			t.Errorf("a refused %q still created a session", name)
		}
		if got := a.store.Session(s.ID).Env; !reflect.DeepEqual(got, []string{"KEEP"}) {
			t.Errorf("a refused %q changed the session to %v", name, got)
		}
	}
	if w := patchSession(t, a, s.ID, `{"env":{"NUL":"a\u0000b"}}`); w.Code != 400 {
		t.Errorf("a value no environment can hold = %d, want 400", w.Code)
	}
}

// A conversation granted names from the agent's environment before keeps them,
// holding what the environment had.
func TestAGrantFromBeforeBecomesAVariable(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghp-granted-before")
	os.Unsetenv("NEVER_SET_ANYWHERE")
	dir := t.TempDir()
	first := newTestAppAt(t, dir)
	s, err := first.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(dir, "sessions", s.ID, "meta.json")
	b, _ := os.ReadFile(metaPath)
	var meta map[string]any
	_ = json.Unmarshal(b, &meta)
	meta["granted_env"] = []string{"GH_TOKEN", "NEVER_SET_ANYWHERE", "OPENROUTER_API_KEY"}
	b, _ = json.Marshal(meta)
	if err := os.WriteFile(metaPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENROUTER_API_KEY", "sk-or-never")

	a := newTestAppAt(t, dir)
	installEnvDump(t, a)
	if got := a.store.Session(s.ID).Env; !reflect.DeepEqual(got, []string{"GH_TOKEN"}) {
		t.Errorf("the grant became %v", got)
	}
	env := envIn(t, a, s.ID)
	if env["GH_TOKEN"] != "ghp-granted-before" {
		t.Errorf("GH_TOKEN = %q, want what the environment held", env["GH_TOKEN"])
	}
	if _, ok := env["OPENROUTER_API_KEY"]; ok {
		t.Error("an old grant of the model key handed it to a tool")
	}
	if b, _ := os.ReadFile(metaPath); strings.Contains(string(b), "granted_env") {
		t.Error("the old grant is still written beside the new variables")
	}
}

// Everything a conversation can be given is given when it starts, as well as
// later: its title and when it compacts.
func TestAConversationStartsAndForksWithItsTitleAndThresholds(t *testing.T) {
	a := newTestApp(t)
	s := start(t, a, `{"model":"test/model","title":"  Deploys  ","compact_at_tokens":60000,"keep_verbatim_tokens":20000}`)
	if s.Title != "Deploys" || s.CompactAtTokens != 60000 || s.KeepVerbatimTokens != 20000 {
		t.Errorf("started as %q at %d keeping %d", s.Title, s.CompactAtTokens, s.KeepVerbatimTokens)
	}
	plain := start(t, a, `{"model":"test/model","title":"   "}`)
	if plain.Title != "New session" || plain.CompactAtTokens != defaultCompactAtTokens || plain.KeepVerbatimTokens != defaultKeepVerbatimTokens {
		t.Errorf("a conversation that named nothing started as %q at %d keeping %d", plain.Title, plain.CompactAtTokens, plain.KeepVerbatimTokens)
	}
	// The screen that starts one says what it would start at, so the numbers it
	// offers are the ones a conversation gets when they are left alone.
	var next struct {
		CompactAtTokens    int `json:"compact_at_tokens"`
		KeepVerbatimTokens int `json:"keep_verbatim_tokens"`
	}
	_ = json.Unmarshal(call(t, a, "GET", "/preferences", "").Body.Bytes(), &next)
	if next.CompactAtTokens != plain.CompactAtTokens || next.KeepVerbatimTokens != plain.KeepVerbatimTokens {
		t.Errorf("/preferences offers %d keeping %d; a new conversation starts at %d keeping %d",
			next.CompactAtTokens, next.KeepVerbatimTokens, plain.CompactAtTokens, plain.KeepVerbatimTokens)
	}

	fork := decodeSession(t, call(t, a, "POST", "/sessions/"+s.ID+"/fork", `{"title":"Branch","compact_at_tokens":8000,"keep_verbatim_tokens":4000}`))
	if fork.Title != "Branch" || fork.CompactAtTokens != 8000 || fork.KeepVerbatimTokens != 4000 {
		t.Errorf("forked as %q at %d keeping %d", fork.Title, fork.CompactAtTokens, fork.KeepVerbatimTokens)
	}
	same := decodeSession(t, call(t, a, "POST", "/sessions/"+s.ID+"/fork", `{"title":""}`))
	if same.Title != "Deploys" || same.CompactAtTokens != 60000 {
		t.Errorf("a fork that named nothing is %q at %d, want its origin's", same.Title, same.CompactAtTokens)
	}

	before := len(a.store.Sessions())
	for _, r := range []struct{ path, body string }{
		{"/sessions", `{"model":"test/model","compact_at_tokens":1000}`},
		{"/sessions/" + s.ID + "/fork", `{"keep_verbatim_tokens":50000}`},
	} {
		if w := call(t, a, "POST", r.path, r.body); w.Code != 400 {
			t.Errorf("POST %s %s = %d, want 400", r.path, r.body, w.Code)
		}
	}
	if n := len(a.store.Sessions()); n != before {
		t.Errorf("a refused start created %d sessions", n-before)
	}
}
