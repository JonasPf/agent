package app

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
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
	writeSkill(t, a, "everyday", "")
	a.skills.Load()
	base := toolAPI(t, a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")

	if code, _ := toolRequest(t, base, "", "GET", "/skills/everyday", ""); code != 401 {
		t.Errorf("a request naming no call got %d, want 401", code)
	}
	if code, _ := toolRequest(t, base, "made-up", "GET", "/skills/everyday", ""); code != 401 {
		t.Errorf("a request naming an invented call got %d, want 401", code)
	}
	token, done := a.calls.issue(s.ID, "")
	if code, body := toolRequest(t, base, token, "GET", "/skills/everyday", ""); code != 200 {
		t.Errorf("a running call was refused: %d %s", code, body)
	}
	done()
	if code, _ := toolRequest(t, base, token, "GET", "/skills/everyday", ""); code != 401 {
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
		{"POST", "/sessions", `{"model":"x/y","env":{"GH_TOKEN":"ghp"}}`},
		{"POST", "/sessions/" + s.ID + "/fork", `{"env":{"GH_TOKEN":"ghp"}}`},
		{"PATCH", "/sessions/" + s.ID, `{"env":{"GH_TOKEN":"ghp"}}`},
		{"POST", "/sessions/" + s.ID + "/messages", `{"text":"push the branch"}`},
		{"POST", "/sessions/import", ``},
		{"GET", "/sessions/" + s.ID + "/transcript", ``},
		{"POST", "/tools/bash/call", `{"command":"true"}`},
		// What the agent's own tools do is not on offer to a program either
		// (ADR-086): a program cannot schedule, search, call a model, or install.
		{"POST", "/jobs", `{"schedule":"10m","prompt":"push the branch"}`},
		{"GET", "/jobs", ``},
		{"GET", "/search?q=token", ``},
		{"GET", "/entry?seq=0", ``},
		{"POST", "/model", `{"model":"x/y","prompt":"hello"}`},
		{"POST", "/packages", `{"install":["hello"]}`},
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
func TestTheScheduleToolReachesOnlyItsOwnConversation(t *testing.T) {
	a := newTestApp(t)
	mine, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	other := start(t, a, `{"model":"test/model","env":{"GH_TOKEN":"ghp"}}`)
	call := func(args string) toolResult {
		t.Helper()
		return a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: mine.ID}, "schedule", json.RawMessage(args))
	}

	res := call(`{"action":"create","session_id":"` + other.ID + `","schedule":"10m","prompt":"push the branch"}`)
	if res.OK || !strings.Contains(res.Error, "its own conversation only") {
		t.Errorf("scheduling into another conversation = %+v, want it refused", res)
	}
	if jobs, _ := a.store.SessionJobs(other.ID); len(jobs) != 0 {
		t.Fatalf("a job was created in the other conversation: %+v", jobs[0])
	}

	if res := call(`{"action":"create","schedule":"10m","prompt":"stretch"}`); !res.OK {
		t.Fatalf("scheduling into its own conversation = %+v", res)
	}
	if jobs, _ := a.store.SessionJobs(mine.ID); len(jobs) != 1 {
		t.Fatalf("the calling conversation has %d jobs, want the one it made", len(jobs))
	}

	theirs, err := a.CreateJob(JobSpec{SessionID: other.ID, Schedule: "10m", Prompt: "check the mail"})
	if err != nil {
		t.Fatal(err)
	}
	if res := call(`{"action":"edit","id":"` + theirs.ID + `","prompt":"push the branch"}`); res.OK || res.Error != "no such job" {
		t.Errorf("editing another conversation's job = %+v, want no such job", res)
	}
	if res := call(`{"action":"delete","id":"` + theirs.ID + `"}`); res.OK || res.Error != "no such job" {
		t.Errorf("deleting another conversation's job = %+v, want no such job", res)
	}
	if j, _ := a.store.Job(theirs.ID); j == nil || j.Prompt != "check the mail" {
		t.Errorf("another conversation's job was changed: %+v", j)
	}

	listed := call(`{"action":"list"}`)
	if !listed.OK || strings.Contains(listed.Content, theirs.ID) || strings.Count(listed.Content, "\n") != 0 ||
		!strings.Contains(listed.Content, "stretch") {
		t.Errorf("listing jobs = %+v, want only the calling conversation's one", listed)
	}
}

// And the same for what a conversation may read back: an entry of its own by
// number, another conversation's only when it names it.
func TestTheSearchToolReadsAnEntryWhole(t *testing.T) {
	a := newTestApp(t)
	mine, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	said := a.append(mine.ID, Entry{Type: "message", Role: "assistant", Text: "the valve is on the left"})
	call := func(args string) toolResult {
		t.Helper()
		return a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: mine.ID}, "session_search", json.RawMessage(args))
	}
	if res := call(`{"seq":` + strconv.Itoa(said.Seq) + `}`); !res.OK || !strings.Contains(res.Content, "the valve is on the left") {
		t.Errorf("reading an entry by number = %+v", res)
	}
	if res := call(`{"seq":9999}`); res.OK || !strings.Contains(res.Error, "no entry 9999") {
		t.Errorf("reading an entry that is not there = %+v", res)
	}
	if res := call(`{"query":"valve"}`); !res.OK || !strings.Contains(res.Content, mine.ID) {
		t.Errorf("searching = %+v, want the entry found", res)
	}
	if res := call(`{}`); res.OK {
		t.Errorf("a search for nothing = %+v, want it refused", res)
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

// End to end: the agent's own tools, called through the registry beside the
// ones that are programs, do their work for the calling conversation only.
func TestTheAgentsOwnToolsWorkThroughTheRegistry(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	a.cfg.ToolsDir = filepath.Join("..", "..", "tools")
	a.tools = NewRegistry(a.cfg.ToolsDir, DBPath(dir), a.store.DB())
	userSkills := t.TempDir()
	writeSkillIn(t, userSkills, "chosen-only", "default: off\n")
	a.skills = NewSkills(filepath.Join("..", "..", "skills"), userSkills)
	if sk := a.skills.Get("chosen-only"); sk == nil || sk.DefaultEnabled {
		t.Fatalf("the skill that is off by default did not load as off: %+v", sk)
	}
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
		{"schedule", `{"action":"create","schedule":"10m","prompt":"stretch","after_acting":"stop"}`},
		{"schedule", `{"action":"list"}`},
		{"skill_read", `{"name":"researching"}`},
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
	if res := call("skill_read", `{"name":"chosen-only"}`); res.OK {
		t.Errorf("skill_read read a skill that is off by default in a conversation that did not choose it")
	}
}
