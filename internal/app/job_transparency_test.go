package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

// fakeModel serves one streamed completion per call, in OpenRouter's SSE shape.
// Each element of replies is the assistant text for one round.
func fakeModel(t *testing.T, replies ...string) *httptest.Server {
	t.Helper()
	n := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := "done"
		if n < len(replies) {
			text = replies[n]
		}
		n++
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": text}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func modelBackedApp(t *testing.T, replies ...string) (*App, *httptest.Server) {
	t.Helper()
	a := newTestApp(t)
	srv := fakeModel(t, replies...)
	t.Cleanup(srv.Close)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	return a, srv
}

func transcriptOf(t *testing.T, a *App, id string) []Entry {
	t.Helper()
	return a.store.Entries(id)
}

// A judgement job used to be run into a buffer and thrown away unless the model
// called notify: ninety-five of ninety-six daily runs vanished, each replaced by
// one line. Nothing the agent does may be discarded before it is stored.
func TestJudgementRunIsRecordedInFull(t *testing.T) {
	a, _ := modelBackedApp(t, "I looked. Nothing has changed.")
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "1h",
		Prompt: "Has anything changed?"})
	if err != nil {
		t.Fatal(err)
	}

	if err := a.runTurn(context.Background(), s, turnOpts{UserText: j.Prompt, JobID: j.ID}); err != nil {
		t.Fatal(err)
	}

	var assistant, user int
	for _, e := range transcriptOf(t, a, s.ID) {
		if e.Type != "message" {
			continue
		}
		switch e.Role {
		case "assistant":
			assistant++
			if !strings.Contains(e.Text, "Nothing has changed") {
				t.Errorf("assistant text = %q, want the model's actual reply", e.Text)
			}
			if e.JobID != j.ID {
				t.Errorf("assistant entry job_id = %q, want %q", e.JobID, j.ID)
			}
		case "user":
			user++
		}
	}
	if user != 1 || assistant != 1 {
		t.Errorf("transcript has %d user and %d assistant messages, want 1 and 1", user, assistant)
	}
}

// What is compiled in is the agent's handle on itself and nothing else
// (ADR-086): the registry's own control surface, and the tools that reach what
// only the agent holds — its jobs, its transcripts, its skills, the model key,
// the package store. A tool that only works on a conversation's files, or the
// web, is not the agent's and is not here.
func TestOnlyTheAgentsOwnHandlesAreCompiledIn(t *testing.T) {
	a := newTestApp(t)
	var builtin []string
	for _, tl := range a.tools.All() {
		if tl.Builtin {
			builtin = append(builtin, tl.Name)
		}
	}
	want := []string{"ask_operator", "call_model", "packages", "reload_tools", "schedule", "session_search", "skill_read"}
	if !slices.Equal(builtin, want) {
		t.Errorf("compiled-in tools = %v, want %v", builtin, want)
	}
	// Each is what its manifest says and does what its code does: a manifest
	// with no code, or code with no manifest, is not a tool.
	if got := len(a.builtinRuns()); got != len(builtinNames()) {
		t.Errorf("%d builtins have code and %d have a manifest", got, len(builtinNames()))
	}
}

// A tool on disk is told the session and job its call belongs to, and nothing
// about where the agent is: it has no address to reach it on and no token to
// name itself with (ADR-086).
func TestToolEnvironmentCarriesSessionAndJobAndNoWayToTheAgent(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	writeTool(t, dir, "envtool", `#!/bin/sh
printf '{"ok":true,"content":"%s%s|%s|%s"}' "$AGENT_URL" "$AGENT_TOKEN" "$AGENT_SESSION" "$AGENT_JOB"
`)
	a.tools.dir = dir
	if _, f := a.tools.Load(a); len(f) > 0 {
		t.Fatalf("load failures: %v", f)
	}
	res := a.tools.Call(context.Background(),
		&ToolCtx{App: a, SessionID: "S1", JobID: "J1"}, "envtool", json.RawMessage(`{}`))
	if !res.OK {
		t.Fatalf("call failed: %s", res.Error)
	}
	want := "|S1|J1"
	if res.Content != want {
		t.Errorf("tool saw %q, want %q", res.Content, want)
	}
}

func writeTool(t *testing.T, root, name, script string) {
	t.Helper()
	d := root + "/" + name
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","description":"Test tool.",` +
		`"parameters":{"type":"object","properties":{}},"agent":{"db_prefix":"` + name + `_"}}`
	if err := os.WriteFile(d+"/manifest.json", []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d+"/run", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
