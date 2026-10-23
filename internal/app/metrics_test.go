package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// toolStep is one round of a scripted turn: a call to a tool, or, with no
// tool, the answer that ends it.
type toolStep struct {
	tool, args, text string
	cost             float64
}

// scriptedModel answers each request with the step its conversation has
// reached, counted by the tool results the request already carries, so a
// title or a second conversation does not move the script along. A request
// without tools is the title being asked for.
func scriptedModel(t *testing.T, steps ...toolStep) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		step := toolStep{text: "A title"}
		if bytes.Contains(body, []byte(`"tools"`)) {
			n := bytes.Count(body, []byte(`"role":"tool"`))
			step = toolStep{text: "done"}
			if n < len(steps) {
				step = steps[n]
			}
		}
		delta := map[string]any{"content": step.text}
		if step.tool != "" {
			delta = map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": fmt.Sprintf("call_%d", time.Now().UnixNano()),
				"function": map[string]any{"name": step.tool, "arguments": step.args},
			}}}
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}},
			"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "cost": step.cost}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// metricsApp is an agent over the tools as shipped and two skills of its own,
// in a state directory the caller can open again.
func metricsApp(t *testing.T, dir string, srv *httptest.Server) *App {
	t.Helper()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: dir, Workspace: filepath.Join(dir, "workspace"), ToolsDir: "../../tools",
		SkillsDir: filepath.Join(dir, "skills")}
	a := &App{cfg: cfg, sandbox: NewSandbox(cfg), store: st,
		tools:  NewRegistry(cfg.ToolsDir, DBPath(dir), st.DB()),
		skills: NewSkills(cfg.SkillsDir, ""), hub: NewHub(),
		queues: map[string]chan func(){}}
	a.registerBuiltins()
	writeSkillIn(t, cfg.SkillsDir, "watering", "")
	writeSkillIn(t, cfg.SkillsDir, "pruning", "")
	a.skills.Load()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %v", failures)
	}
	stop, err := a.listenTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if srv != nil {
		a.or = NewOpenRouter("test-key")
		a.or.base = srv.URL
	}
	return a
}

func metricsOf(t *testing.T, a *App, query string) Metrics {
	t.Helper()
	w := callAPI(t, a, "GET", "/metrics"+query, nil)
	if w.Code != 200 {
		t.Fatalf("GET /metrics%s = %d: %s", query, w.Code, w.Body.String())
	}
	var m Metrics
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func toolIn(m Metrics, name string) ToolMetric {
	for _, t := range m.Tools {
		if t.Name == name {
			return t
		}
	}
	return ToolMetric{}
}

func skillIn(m Metrics, name string) SkillMetric {
	for _, s := range m.Skills {
		if s.Name == name {
			return s
		}
	}
	return SkillMetric{}
}

func jobIn(m Metrics, id string) (JobMetric, bool) {
	for _, j := range m.Jobs {
		if j.ID == id {
			return j, true
		}
	}
	return JobMetric{}, false
}

// Which skills are read, and which tools fail, was answered once by copying
// every transcript off the server and counting by hand. The agent counts it
// itself now, from the transcripts it already holds: a skill read is a
// skill_read call that succeeded, a failure is a tool result that says so, and
// a skill is offered to every conversation whose index listed it.
func TestMetricsCountTheSkillsReadAndTheToolsThatFailed(t *testing.T) {
	srv := scriptedModel(t,
		toolStep{tool: "skill_read", args: `{"name":"watering"}`},
		toolStep{tool: "read", args: `{"path":"no-such-file.txt"}`},
		toolStep{text: "Water twice a week."})
	a := metricsApp(t, t.TempDir(), srv)
	s := newSession(t, a)
	say(t, a, s.ID, "How often should I water the tomatoes?")
	quiet := newSession(t, a) // shown both skills, but never spoken in

	m := metricsOf(t, a, "")
	if m.Conversations != 1 {
		t.Errorf("conversations = %d, want 1: one that was never spoken in shows its model nothing", m.Conversations)
	}
	if got := toolIn(m, "skill_read"); got.Calls != 1 || got.Failures != 0 || got.Conversations != 1 {
		t.Errorf("skill_read = %+v, want one call that succeeded, in one conversation", got)
	}
	read := toolIn(m, "read")
	if read.Calls != 1 || read.Failures != 1 {
		t.Errorf("read = %+v, want one call that failed", read)
	}
	if !strings.Contains(read.LastError, "no-such-file.txt") {
		t.Errorf("read's last failure is %q; want what it said", read.LastError)
	}
	if read.LastAt.IsZero() {
		t.Error("read was called and has no time it was last called")
	}
	if got := toolIn(m, "bash"); got.Name != "bash" || got.Calls != 0 || !got.Loaded {
		t.Errorf("bash = %+v; a loaded tool nobody called is listed, with no calls", got)
	}

	if got := skillIn(m, "watering"); got.Reads != 1 || got.Conversations != 1 || got.Offered != 1 || !got.Loaded {
		t.Errorf("watering = %+v, want read once, in the one conversation it was offered to", got)
	}
	if got := skillIn(m, "pruning"); got.Name != "pruning" || got.Reads != 0 || got.Offered != 1 {
		t.Errorf("pruning = %+v, want offered once and never read", got)
	}

	// A fork carries the conversation over, and what it carried happened once.
	w := callAPI(t, a, "POST", "/sessions/"+s.ID+"/fork", map[string]any{})
	if w.Code != 201 {
		t.Fatalf("fork = %d: %s", w.Code, w.Body.String())
	}
	var fork Session
	if err := json.Unmarshal(w.Body.Bytes(), &fork); err != nil {
		t.Fatal(err)
	}
	if got := toolIn(metricsOf(t, a, ""), "read"); got.Calls != 1 {
		t.Errorf("after a fork read has %d calls, want 1: the copy was counted again", got.Calls)
	}

	// A skill that is gone keeps the reads it had.
	if err := os.Remove(filepath.Join(a.cfg.SkillsDir, "watering.md")); err != nil {
		t.Fatal(err)
	}
	a.skills.Load()
	if got := skillIn(metricsOf(t, a, ""), "watering"); got.Reads != 1 || got.Loaded {
		t.Errorf("a deleted skill = %+v, want its read kept and marked not loaded", got)
	}

	// The numbers are the transcripts': a conversation deleted takes its own.
	for _, id := range []string{s.ID, quiet.ID, fork.ID} {
		if w := callAPI(t, a, "DELETE", "/sessions/"+id, nil); w.Code >= 300 {
			t.Fatalf("delete = %d: %s", w.Code, w.Body.String())
		}
	}
	if got := toolIn(metricsOf(t, a, ""), "read"); got.Calls != 0 {
		t.Errorf("read has %d calls after every conversation that made them was deleted", got.Calls)
	}
}

// A job is asked two things: how often it acts, and what that costs. A wake
// whose check said no costs nothing and is counted apart from one that acted.
func TestMetricsCountAJobsWakesAndWhatTheyCost(t *testing.T) {
	srv := scriptedModel(t, toolStep{text: "Reminder: feed the cat.", cost: 0.25})
	a := metricsApp(t, t.TempDir(), srv)
	a.sched = NewScheduler(a)
	s := newSession(t, a)
	acts, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Remind me to feed the cat.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	waits, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m", Check: "false",
		Prompt: "Tell me when the parcel arrives.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range []*Job{acts, waits} {
		j.NextRunAt = time.Now().Add(-time.Second)
		if err := a.store.PutJob(j); err != nil {
			t.Fatal(err)
		}
	}
	// One wake at a time goes into a conversation, so the second waits a tick.
	for range 2 {
		a.sched.tick(context.Background())
		settle(t, a, s.ID)
	}

	m := metricsOf(t, a, "")
	got, ok := jobIn(m, acts.ID)
	if !ok {
		t.Fatalf("the job that acted is not listed: %+v", m.Jobs)
	}
	if got.Fired != 1 || got.Skipped != 0 || got.Failed != 0 {
		t.Errorf("the job that acted = %+v, want one wake that fired", got)
	}
	if got.Cost < 0.249 || got.Cost > 0.251 {
		t.Errorf("the job that acted cost %v, want 0.25: what its wake's model calls cost", got.Cost)
	}
	if got.Prompt != acts.Prompt || got.SessionID != s.ID {
		t.Errorf("the job is not named by its prompt and conversation: %+v", got)
	}
	idle, ok := jobIn(m, waits.ID)
	if !ok || idle.Skipped != 1 || idle.Fired != 0 || idle.Cost != 0 {
		t.Errorf("the job whose check said no = %+v (listed %v), want one skipped wake that cost nothing", idle, ok)
	}
}

// A window counts what happened in it. The skill_read prompt changed on
// 2026-10-22, and whether skills are read more since is a question about the
// last week, not about every conversation there has ever been.
func TestMetricsCountOnlyWhatHappenedInTheWindow(t *testing.T) {
	dir := t.TempDir()
	srv := scriptedModel(t,
		toolStep{tool: "skill_read", args: `{"name":"watering"}`},
		toolStep{text: "Water twice a week.", cost: 0.25})
	a := metricsApp(t, dir, srv)
	a.sched = NewScheduler(a)
	s := newSession(t, a)
	say(t, a, s.ID, "How often should I water the tomatoes?")
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Remind me to water.", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}
	a.sched.tick(context.Background())
	settle(t, a, s.ID)

	// Forty days pass.
	old := time.Now().Add(-40 * 24 * time.Hour)
	path := transcriptPath(dir, s.ID)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var e map[string]any
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		e["created_at"] = old
		line, _ := json.Marshal(e)
		out.Write(append(line, '\n'))
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB().Exec(`update job_runs set at=?`, old); err != nil {
		t.Fatal(err)
	}

	again := metricsApp(t, dir, nil)
	week := metricsOf(t, again, "?days=7")
	if week.Days != 7 || week.Since.IsZero() {
		t.Errorf("the week's metrics say days=%d since=%v", week.Days, week.Since)
	}
	if got := toolIn(week, "skill_read"); got.Calls != 0 {
		t.Errorf("skill_read has %d calls in the last week; every one was forty days ago", got.Calls)
	}
	if got := skillIn(week, "watering"); got.Reads != 0 || got.Offered != 0 {
		t.Errorf("watering in the last week = %+v; the conversation it was read in has been quiet since", got)
	}
	if got, ok := jobIn(week, j.ID); !ok || got.Fired != 0 || got.Cost != 0 {
		t.Errorf("the job in the last week = %+v (listed %v); still scheduled, but nothing in the week", got, ok)
	}

	all := metricsOf(t, again, "")
	if got := skillIn(all, "watering"); got.Reads != 1 || got.Offered != 1 {
		t.Errorf("watering over all time = %+v, want the read forty days ago", got)
	}
	if got, _ := jobIn(all, j.ID); got.Fired != 1 || got.Cost < 0.249 {
		t.Errorf("the job over all time = %+v, want its wake forty days ago", got)
	}

	if w := callAPI(t, again, "GET", "/metrics?days=soon", nil); w.Code != 400 {
		t.Errorf("a window that is not a number of days = %d, want 400", w.Code)
	}
}
