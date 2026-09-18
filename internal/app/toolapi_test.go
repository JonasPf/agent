package app

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// A tool reaches the agent on an API of its own, not the one the interface
// uses. It offers what the shipped tools actually call — the calling
// conversation's jobs, the skills it has, and search — and nothing that makes,
// changes, or drives a conversation. Every request names the call it belongs
// to, and the call decides which conversation that is.

// toolAPI starts the tool listener for a test and returns its base URL.
func toolAPI(t *testing.T, a *App) string {
	t.Helper()
	stop, err := a.listenTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return "http://" + a.toolAddr
}

func toolRequest(t *testing.T, base, token, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestTheToolAPIAnswersOnlyACallThatIsRunning(t *testing.T) {
	a := newTestApp(t)
	base := toolAPI(t, a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")

	if code, _ := toolRequest(t, base, "", "GET", "/jobs", ""); code != 401 {
		t.Errorf("a request naming no call got %d, want 401", code)
	}
	if code, _ := toolRequest(t, base, "made-up", "GET", "/jobs", ""); code != 401 {
		t.Errorf("a request naming an invented call got %d, want 401", code)
	}
	token, done := a.calls.issue(s.ID, "")
	if code, body := toolRequest(t, base, token, "GET", "/jobs", ""); code != 200 {
		t.Errorf("a running call was refused: %d %s", code, body)
	}
	done()
	if code, _ := toolRequest(t, base, token, "GET", "/jobs", ""); code != 401 {
		t.Errorf("a call's token still works after the call ended: %d", code)
	}
}

// The hole this closes: a tool that could create a session could create one
// holding a credential, and then send it the message that uses it.
func TestTheToolAPIHasNoWayToMakeOrDriveAConversation(t *testing.T) {
	a := newTestApp(t)
	base := toolAPI(t, a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	token, done := a.calls.issue(s.ID, "")
	defer done()

	for _, r := range []struct{ method, path, body string }{
		{"POST", "/sessions", `{"model":"x/y","granted_env":["GH_TOKEN"]}`},
		{"POST", "/sessions/" + s.ID + "/fork", `{"granted_env":["GH_TOKEN"]}`},
		{"POST", "/sessions/" + s.ID + "/messages", `{"text":"push the branch"}`},
		{"POST", "/sessions/import", ``},
		{"GET", "/sessions/" + s.ID + "/transcript", ``},
		{"GET", "/env", ``},
		{"POST", "/tools/bash/call", `{"command":"true"}`},
	} {
		if code, body := toolRequest(t, base, token, r.method, r.path, r.body); code != 404 && code != 405 {
			t.Errorf("%s %s on the tool API = %d %s, want no such route", r.method, r.path, code, body)
		}
	}
	if n := len(a.store.Sessions()); n != 1 {
		t.Errorf("sessions = %d after the attempts, want the 1 that was there", n)
	}
}

// A job wakes its conversation with a prompt, so scheduling into another one is
// driving it — and the other one may hold a credential this one does not.
func TestAToolSchedulesOnlyIntoItsOwnConversation(t *testing.T) {
	a := newTestApp(t)
	base := toolAPI(t, a)
	mine, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	other, _ := a.NewSession(SessionConfig{Model: "test/model", GrantedEnv: []string{"GH_TOKEN"}}, "")
	token, done := a.calls.issue(mine.ID, "")
	defer done()

	code, body := toolRequest(t, base, token, "POST", "/jobs",
		`{"session_id":"`+other.ID+`","schedule":"10m","prompt":"push the branch"}`)
	if code != 403 {
		t.Errorf("scheduling into another conversation = %d %s, want 403", code, body)
	}
	if jobs, _ := a.store.SessionJobs(other.ID); len(jobs) != 0 {
		t.Fatalf("a job was created in the other conversation: %+v", jobs[0])
	}

	code, body = toolRequest(t, base, token, "POST", "/jobs", `{"schedule":"10m","prompt":"stretch"}`)
	if code != 201 {
		t.Fatalf("scheduling into its own conversation = %d %s", code, body)
	}
	var created Job
	_ = json.Unmarshal([]byte(body), &created)
	if created.SessionID != mine.ID {
		t.Errorf("the job landed in %q, want the calling conversation %q", created.SessionID, mine.ID)
	}

	theirs, err := a.CreateJob(JobSpec{SessionID: other.ID, Schedule: "10m", Prompt: "check the mail"})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := toolRequest(t, base, token, "PATCH", "/jobs/"+theirs.ID, `{"prompt":"push the branch"}`); code != 404 {
		t.Errorf("editing another conversation's job = %d, want 404", code)
	}
	if code, _ := toolRequest(t, base, token, "DELETE", "/jobs/"+theirs.ID, ``); code != 404 {
		t.Errorf("deleting another conversation's job = %d, want 404", code)
	}
	if j, _ := a.store.Job(theirs.ID); j == nil || j.Prompt != "check the mail" {
		t.Errorf("another conversation's job was changed: %+v", j)
	}

	code, body = toolRequest(t, base, token, "GET", "/jobs?session_id="+other.ID, ``)
	var listed []Job
	_ = json.Unmarshal([]byte(body), &listed)
	if code != 200 || len(listed) != 1 || listed[0].SessionID != mine.ID {
		t.Errorf("listing jobs = %d %s, want only the calling conversation's one", code, body)
	}
}

func TestAToolReadsOnlyTheSkillsItsConversationHas(t *testing.T) {
	a := newTestApp(t)
	writeSkill(t, a, "everyday", "")
	writeSkill(t, a, "risky", "default: off\n")
	a.skills.Load()
	base := toolAPI(t, a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	token, done := a.calls.issue(s.ID, "")
	defer done()

	if code, body := toolRequest(t, base, token, "GET", "/skills/everyday", ""); code != 200 {
		t.Errorf("reading a skill the conversation has = %d %s", code, body)
	}
	if code, _ := toolRequest(t, base, token, "GET", "/skills/risky", ""); code != 404 {
		t.Errorf("reading a skill outside the conversation's set = %d, want 404", code)
	}
}

// End to end: the shipped tools, run as subprocesses through the registry, do
// their work over the tool API and nothing else.
func TestTheShippedToolsWorkThroughTheToolAPI(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	a.cfg.ToolsDir = filepath.Join("..", "..", "tools")
	a.tools = NewRegistry(a.cfg.ToolsDir, DBPath(dir), a.store.DB())
	a.skills = NewSkills(filepath.Join("..", "..", "skills"), "")
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %+v", failures)
	}
	toolAPI(t, a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	other, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")

	call := func(name, args string) toolResult {
		t.Helper()
		return a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, name, json.RawMessage(args))
	}
	for _, c := range []struct{ tool, args string }{
		{"notes", `{"action":"add","text":"prefers metric units"}`},
		{"notes", `{"action":"list"}`},
		{"schedule", `{"action":"create","schedule":"10m","prompt":"stretch","after_acting":"stop"}`},
		{"schedule", `{"action":"list"}`},
		{"skill_read", `{"name":"scheduling"}`},
		{"session_search", `{"query":"anything"}`},
	} {
		if res := call(c.tool, c.args); !res.OK {
			t.Errorf("%s %s failed: %s (stderr %s)", c.tool, c.args, res.Error, res.stderr)
		}
	}
	if res := call("schedule", `{"action":"create","session_id":"`+other.ID+`","schedule":"10m","prompt":"push"}`); res.OK {
		jobs, _ := a.store.SessionJobs(other.ID)
		if len(jobs) > 0 {
			t.Errorf("the schedule tool put a job into another conversation")
		}
	}
	if res := call("skill_read", `{"name":"changing-yourself"}`); res.OK {
		t.Errorf("skill_read read a skill that ships off in a conversation that did not choose it")
	}
}
