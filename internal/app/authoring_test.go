package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The operator writes skills and personas through the interface, and the agent
// still cannot: the roots they are written to are outside every path the sandbox
// makes writable (ADR-053). These drive the real routes.

func callAPI(t *testing.T, a *App, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

// promptSection is one section of a session's prompt entry as sent, and whether
// it is there at all.
func promptSection(t *testing.T, a *App, s *Session, name string) (string, bool) {
	t.Helper()
	p, ok := a.promptEntry(s.ID)
	if !ok {
		t.Fatal("the session has no prompt")
	}
	for _, sec := range p.Sections {
		if sec.Name == name {
			return sec.Text, true
		}
	}
	return "", false
}

func TestAnUploadedSkillIsIndexedInTheSessionsStartedAfterIt(t *testing.T) {
	a := newTestApp(t)

	w := callAPI(t, a, "POST", "/skills", map[string]any{
		"name": "watering", "description": "When to water the tomatoes.",
		"body": "Twice a week, more in August.",
	})
	if w.Code != 200 {
		t.Fatalf("POST /skills: status %d (%s)", w.Code, w.Body.String())
	}

	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	idx, ok := promptSection(t, a, s, "skills_index")
	if !ok {
		t.Fatal("the prompt has no skills index")
	}
	if !strings.Contains(idx, "watering") || !strings.Contains(idx, "When to water the tomatoes.") {
		t.Errorf("an uploaded skill is not in the index: %q", idx)
	}

	// And its body is what skill_read will return.
	w = callAPI(t, a, "GET", "/skills/watering", nil)
	var got struct {
		Body     string `json:"body"`
		Editable bool   `json:"editable"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if !strings.Contains(got.Body, "Twice a week") {
		t.Errorf("body = %q, want the text that was uploaded", got.Body)
	}
	if !got.Editable {
		t.Error("a skill the operator wrote reports itself as not editable")
	}
}

// The whole point of writing to the data directory rather than the image: a
// skill uploaded here is read back from disk by a fresh load, so it survives the
// process that wrote it.
func TestAnUploadedSkillIsOnDiskAndSurvivesAReload(t *testing.T) {
	a := newTestApp(t)
	if w := callAPI(t, a, "POST", "/skills", map[string]any{
		"name": "watering", "description": "When to water.", "body": "Twice a week.",
	}); w.Code != 200 {
		t.Fatalf("POST /skills: status %d (%s)", w.Code, w.Body.String())
	}
	path := filepath.Join(a.skills.UserDir(), "watering.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the skill is not on disk: %v", err)
	}
	fresh := NewSkills(a.skills.shipped, a.skills.UserDir())
	if sk := fresh.Get("watering"); sk == nil {
		t.Error("a skill written through the interface does not load from disk")
	} else if !sk.Editable {
		t.Error("a skill in the operator's root loads as not editable")
	}
}

func TestAnUploadedSkillCanBeEditedAndDeleted(t *testing.T) {
	a := newTestApp(t)
	callAPI(t, a, "POST", "/skills", map[string]any{
		"name": "watering", "description": "When to water.", "body": "Twice a week.",
	})

	if w := callAPI(t, a, "PUT", "/skills/watering", map[string]any{
		"description": "When to water, revised.", "body": "Three times a week.",
		"default_enabled": false,
	}); w.Code != 200 {
		t.Fatalf("PUT: status %d (%s)", w.Code, w.Body.String())
	}
	sk := a.skills.Get("watering")
	if sk == nil {
		t.Fatal("the skill is gone after an edit")
	}
	if sk.Description != "When to water, revised." || !strings.Contains(sk.Body, "Three times") {
		t.Errorf("edit did not take: %q / %q", sk.Description, sk.Body)
	}
	// default: off must round-trip through the file, or a skill the operator
	// turned off comes back on at the next load.
	if sk.DefaultEnabled {
		t.Error("default_enabled=false did not survive the write")
	}
	fresh := NewSkills(a.skills.shipped, a.skills.UserDir())
	if got := fresh.Get("watering"); got == nil || got.DefaultEnabled {
		t.Error("default: off was not written to the file")
	}

	if w := callAPI(t, a, "DELETE", "/skills/watering", nil); w.Code != 204 {
		t.Fatalf("DELETE: status %d (%s)", w.Code, w.Body.String())
	}
	if a.skills.Get("watering") != nil {
		t.Error("the skill is still loaded after being deleted")
	}
	if _, err := os.Stat(filepath.Join(a.skills.UserDir(), "watering.md")); !os.IsNotExist(err) {
		t.Errorf("the file is still on disk after a delete: %v", err)
	}
}

// A skill that ships in the image is read-only. Writing a copy into the
// operator's root would shadow it until the next deployment restored the
// original, and nothing on screen could explain the change.
func TestAShippedSkillCannotBeEditedOrDeleted(t *testing.T) {
	a := newTestApp(t)
	writeSkill(t, a, "scheduling", "")
	a.skills.Load()

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/skills/scheduling", map[string]any{"description": "Mine now.", "body": "Replaced."}},
		{"DELETE", "/skills/scheduling", nil},
	} {
		w := callAPI(t, a, c.method, c.path, c.body)
		if w.Code != 409 {
			t.Errorf("%s %s: status %d, want 409 (%s)", c.method, c.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "ships with the agent") {
			t.Errorf("%s %s: %q does not say why", c.method, c.path, w.Body.String())
		}
	}
	if sk := a.skills.Get("scheduling"); sk == nil || strings.Contains(sk.Body, "Replaced") {
		t.Error("the shipped skill was changed")
	}
}

// A name is an identifier. Two skills answering to one would make the index a
// lie about what skill_read returns, so the collision is refused and said out
// loud rather than resolved silently.
func TestAnUploadedSkillCannotTakeAShippedSkillsName(t *testing.T) {
	a := newTestApp(t)
	writeSkill(t, a, "scheduling", "")
	a.skills.Load()

	w := callAPI(t, a, "POST", "/skills", map[string]any{
		"name": "scheduling", "description": "Mine.", "body": "Replaced.",
	})
	if w.Code != 409 {
		t.Fatalf("status %d, want 409 (%s)", w.Code, w.Body.String())
	}

	// And a file put there by hand loses to the shipped one, visibly.
	writeSkillIn(t, a.skills.UserDir(), "scheduling", "")
	a.skills.Load()
	if sk := a.skills.Get("scheduling"); sk == nil || sk.Editable {
		t.Error("a hand-written collision displaced the shipped skill")
	}
	var named bool
	for _, f := range a.skills.Failures() {
		if strings.Contains(f.Reason, "ships with the agent") {
			named = true
		}
	}
	if !named {
		t.Errorf("the collision is not reported as a failure: %+v", a.skills.Failures())
	}
}

func TestASkillMissingWhatItNeedsIsRefusedWithAReason(t *testing.T) {
	a := newTestApp(t)
	for _, c := range []struct {
		name string
		body map[string]any
	}{
		{"no name", map[string]any{"description": "d", "body": "b"}},
		{"no description", map[string]any{"name": "x", "body": "b"}},
		{"no body", map[string]any{"name": "x", "description": "d"}},
		{"a description over two lines", map[string]any{"name": "x", "description": "one\ntwo", "body": "b"}},
		{"a name that climbs out", map[string]any{"name": "../escape", "description": "d", "body": "b"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := callAPI(t, a, "POST", "/skills", c.body)
			if w.Code != 409 {
				t.Fatalf("status %d, want 409 (%s)", w.Code, w.Body.String())
			}
			if len(a.skills.All()) != 0 {
				t.Errorf("a refused skill was stored anyway: %+v", a.skills.All())
			}
		})
	}
}

// The operator's roots must stay outside what a tool can write, or "the operator
// writes these, the agent does not" becomes a rule the agent is merely asked to
// respect. The sandbox policy is the enforcement, and this is the claim about it.
func TestTheOperatorsRootsAreOutsideEveryPathAToolCanWrite(t *testing.T) {
	cfg := Config{
		DataDir: filepath.Join("/srv/state", "data"), Workspace: filepath.Join("/srv/state", "workspace"),
		ToolsDir: "/srv/tools", SkillsDir: "/srv/skills",
		UserSkillsDir: filepath.Join("/srv/state", "skills"),
		PersonasDir:   filepath.Join("/srv/state", "personas"),
	}
	reach := NewSandbox(cfg).Reach("", nil)
	for _, dir := range []string{cfg.UserSkillsDir, cfg.PersonasDir, cfg.SkillsDir} {
		for _, w := range reach.ReadWrite {
			if w == dir || strings.HasPrefix(dir, strings.TrimSuffix(w, "/")+"/") {
				t.Errorf("a tool can write %s, which is the operator's to write (writable: %v)", dir, reach.ReadWrite)
			}
		}
	}
}
