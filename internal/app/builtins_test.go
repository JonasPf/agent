package app

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The agent's handles on itself — jobs, search, skills — are tools built into
// it (ADR-086). Each acts for the conversation that called it and no other. A
// program has no way to ask the agent anything.

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

func TestAConversationReadsOnlyTheSkillsItHas(t *testing.T) {
	a := newTestApp(t)
	writeSkill(t, a, "everyday", "")
	writeSkill(t, a, "risky", "default: off\n")
	a.skills.Load()
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")

	if !readsSkill(t, a, s, "everyday") {
		t.Error("a conversation cannot read a skill it has")
	}
	if readsSkill(t, a, s, "risky") {
		t.Error("a conversation read a skill outside its set")
	}
}

// readsSkill says whether a conversation's skill_read call reads a skill.
func readsSkill(t *testing.T, a *App, s *Session, name string) bool {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"name": name})
	return a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "skill_read", args).OK
}

// End to end: the agent's own tools, called through the registry beside the
// ones that are programs, do their work for the calling conversation only.
func TestTheAgentsOwnToolsWorkThroughTheRegistry(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	a.cfg.PluginsDir = shippedBundles(t)
	a.tools = NewRegistry(a.cfg.PluginsDir)
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
