package app

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Metrics are how much each tool, skill, and job is used. They are counted
// from what is already stored, on the way out, rather than recorded as they
// happen: the transcript is what the agent did, and a counter kept beside it
// could disagree with it. So what a deleted conversation did leaves the
// numbers with it, as what a deleted job did leaves with its log.
type Metrics struct {
	// Days is the window asked for, and Since where it begins. No window is
	// every conversation there is.
	Days  int       `json:"days"`
	Since time.Time `json:"since,omitzero"`
	// Conversations is how many had a model call in the window, which is what
	// every "in how many conversations" below is out of.
	Conversations int           `json:"conversations"`
	Tools         []ToolMetric  `json:"tools"`
	Skills        []SkillMetric `json:"skills"`
	Jobs          []JobMetric   `json:"jobs"`
}

type ToolMetric struct {
	Name          string    `json:"name"`
	Calls         int       `json:"calls"`
	Failures      int       `json:"failures"`
	Conversations int       `json:"conversations"`
	LastAt        time.Time `json:"last_at,omitzero"`
	// LastError is what the newest failure said, which is usually enough to
	// tell a tool that is broken from one the model calls wrongly.
	LastError string `json:"last_error,omitempty"`
	Loaded    bool   `json:"loaded"`
}

type SkillMetric struct {
	Name  string `json:"name"`
	Reads int    `json:"reads"`
	// Conversations read it; Offered is how many listed it in the index their
	// model was shown. A skill read in few of the conversations offered it is
	// one whose description is not doing its job.
	Conversations int       `json:"conversations"`
	Offered       int       `json:"offered"`
	LastAt        time.Time `json:"last_at,omitzero"`
	Loaded        bool      `json:"loaded"`
}

type JobMetric struct {
	ID           string `json:"id"`
	SessionID    string `json:"session_id"`
	SessionTitle string `json:"session_title"`
	Prompt       string `json:"prompt"`
	Schedule     string `json:"schedule"`
	Check        string `json:"check,omitempty"`
	Status       string `json:"status"`
	// Fired, Skipped, and Failed are its wakes by outcome. Cost is what the
	// model calls of the wakes that fired cost; a check costs nothing.
	Fired   int       `json:"fired"`
	Skipped int       `json:"skipped"`
	Failed  int       `json:"failed"`
	Cost    float64   `json:"cost"`
	LastAt  time.Time `json:"last_at,omitzero"`
}

func (a *App) hMetrics(w http.ResponseWriter, r *http.Request) {
	days := 0
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			fail(w, 400, "days is a whole number of days, or 0 for all of them")
			return
		}
		days = n
	}
	var since time.Time
	if days > 0 {
		since = time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	}
	m, err := a.Metrics(since)
	if err != nil {
		fail(w, 500, "%v", err)
		return
	}
	m.Days = days
	writeJSON(w, 200, m)
}

// Metrics counts what happened since the given time, or ever, for a zero one.
func (a *App) Metrics(since time.Time) (Metrics, error) {
	m := Metrics{Since: since}
	tools := map[string]*ToolMetric{}
	tool := func(name string) *ToolMetric {
		if tools[name] == nil {
			tools[name] = &ToolMetric{Name: name}
		}
		return tools[name]
	}
	skills := map[string]*SkillMetric{}
	skill := func(name string) *SkillMetric {
		if skills[name] == nil {
			skills[name] = &SkillMetric{Name: name}
		}
		return skills[name]
	}
	jobCost := map[string]float64{}
	lastFailed := map[string]time.Time{}

	for _, sess := range a.store.Sessions() {
		var offered []string
		calls := map[string]ToolCall{}
		usedTools, readSkills := map[string]bool{}, map[string]bool{}
		active := false
		for _, e := range a.store.Entries(sess.ID) {
			if e.Type == "prompt" {
				offered = append(offered, indexedSkills(e)...)
				continue
			}
			for _, c := range e.ToolCalls {
				calls[c.ID] = c
			}
			// What a fork carried over happened once, in the conversation it
			// was carried from.
			if e.CarriedFrom != "" || e.Type != "message" || e.CreatedAt.Before(since) {
				continue
			}
			switch e.Role {
			case "assistant":
				active = true
				if e.JobID != "" && e.Usage != nil {
					jobCost[e.JobID] += e.Usage.Cost
				}
			case "tool":
				var res toolResult
				_ = json.Unmarshal(e.ToolResult, &res)
				t := tool(e.ToolName)
				t.Calls++
				usedTools[e.ToolName] = true
				if !res.OK {
					t.Failures++
					if !e.CreatedAt.Before(lastFailed[e.ToolName]) {
						lastFailed[e.ToolName] = e.CreatedAt
						t.LastError = truncate(res.Error, 300)
					}
				}
				t.LastAt = latest(t.LastAt, e.CreatedAt)
				if e.ToolName != "skill_read" || !res.OK {
					continue
				}
				var args struct {
					Name string `json:"name"`
				}
				if json.Unmarshal([]byte(calls[e.ToolCallID].Arguments), &args) != nil || args.Name == "" {
					continue
				}
				sk := skill(args.Name)
				sk.Reads++
				sk.LastAt = latest(sk.LastAt, e.CreatedAt)
				readSkills[args.Name] = true
			}
		}
		if !active {
			continue
		}
		m.Conversations++
		for name := range usedTools {
			tool(name).Conversations++
		}
		for name := range readSkills {
			skill(name).Conversations++
		}
		seen := map[string]bool{}
		for _, name := range offered {
			if !seen[name] {
				seen[name] = true
				skill(name).Offered++
			}
		}
	}

	// What is loaded is listed whether or not it was used: a tool nobody calls
	// is the answer to a question too.
	for _, t := range a.tools.All() {
		tool(t.Name).Loaded = true
	}
	for _, s := range a.skills.All() {
		skill(s.Name).Loaded = true
	}
	for _, t := range tools {
		m.Tools = append(m.Tools, *t)
	}
	sort.Slice(m.Tools, func(i, j int) bool {
		if m.Tools[i].Calls != m.Tools[j].Calls {
			return m.Tools[i].Calls > m.Tools[j].Calls
		}
		return m.Tools[i].Name < m.Tools[j].Name
	})
	for _, s := range skills {
		m.Skills = append(m.Skills, *s)
	}
	sort.Slice(m.Skills, func(i, j int) bool {
		if m.Skills[i].Reads != m.Skills[j].Reads {
			return m.Skills[i].Reads > m.Skills[j].Reads
		}
		return m.Skills[i].Name < m.Skills[j].Name
	})

	jobs, err := a.store.Jobs()
	if err != nil {
		return m, err
	}
	m.Jobs = []JobMetric{}
	for _, j := range jobs {
		runs, err := a.store.JobRuns(j.ID)
		if err != nil {
			return m, err
		}
		jm := JobMetric{ID: j.ID, SessionID: j.SessionID, Prompt: j.Prompt, Schedule: j.Schedule,
			Check: j.Check, Status: j.Status, Cost: jobCost[j.ID]}
		if s := a.store.Session(j.SessionID); s != nil {
			jm.SessionTitle = s.Title
		}
		for _, r := range runs {
			if r.At.Before(since) {
				continue
			}
			switch r.Outcome {
			case jobFired:
				jm.Fired++
			case jobSkipped:
				jm.Skipped++
			case jobFailed:
				jm.Failed++
			}
			jm.LastAt = latest(jm.LastAt, r.At)
		}
		// A finished job that did nothing in the window has nothing to say in
		// it; one still scheduled is listed, since not running is an answer.
		if jm.Fired+jm.Skipped+jm.Failed == 0 && j.Status == jobDone {
			continue
		}
		m.Jobs = append(m.Jobs, jm)
	}
	sort.SliceStable(m.Jobs, func(i, j int) bool {
		if m.Jobs[i].Cost != m.Jobs[j].Cost {
			return m.Jobs[i].Cost > m.Jobs[j].Cost
		}
		return m.Jobs[i].Fired+m.Jobs[i].Skipped+m.Jobs[i].Failed >
			m.Jobs[j].Fired+m.Jobs[j].Skipped+m.Jobs[j].Failed
	})
	return m, nil
}

// indexedSkills is the skills a prompt entry's index listed, which is what the
// conversation was offered: the prompt is fixed for a conversation's life, so
// what is loaded now may not be what it was shown.
func indexedSkills(prompt Entry) []string {
	var out []string
	for _, sec := range prompt.Sections {
		if sec.Name != "skills_index" {
			continue
		}
		for _, line := range strings.Split(sec.Text, "\n") {
			rest, listed := strings.CutPrefix(line, "- ")
			if name, _, ok := strings.Cut(rest, ": "); listed && ok {
				out = append(out, name)
			}
		}
	}
	return out
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
