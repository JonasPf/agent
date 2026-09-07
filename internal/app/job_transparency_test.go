package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

// The kernel is the loop, the scheduler, the store, the gateway, and the tool
// registry. reload_tools is the registry's own control surface and cannot be a
// tool that the registry has to load first. Nothing else is compiled in.
func TestOnlyTheRegistryControlSurfaceIsCompiledIn(t *testing.T) {
	a := newTestApp(t)
	var builtin []string
	for _, tl := range a.tools.All() {
		if tl.Builtin {
			builtin = append(builtin, tl.Name)
		}
	}
	if len(builtin) != 1 || builtin[0] != "reload_tools" {
		t.Errorf("compiled-in tools = %v, want [reload_tools]", builtin)
	}
}

// A tool on disk reaches the system the way the interface does, so it is given
// the API base URL and the session and job the call belongs to. Without these a
// capability could only be compiled in, which is what R4 forbids.
func TestToolEnvironmentCarriesAPIBaseSessionAndJob(t *testing.T) {
	a := newTestApp(t)
	a.cfg.Addr = ":9931"
	dir := t.TempDir()
	writeTool(t, dir, "envtool", `#!/bin/sh
printf '{"ok":true,"content":"%s|%s|%s"}' "$AGENT_URL" "$AGENT_SESSION" "$AGENT_JOB"
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
	want := "http://127.0.0.1:9931|S1|J1"
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
	manifest := `{"name":"` + name + `","description":"Test tool.","db_prefix":"` + name +
		`_","parameters":{"type":"object","properties":{}}}`
	if err := os.WriteFile(d+"/manifest.json", []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d+"/run", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
