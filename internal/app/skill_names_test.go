package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A skill's name is lowercase words joined by hyphens, as the Agent Skills
// format names one, so a skill written here is a skill elsewhere too. A
// tool's is lowercase words joined by underscores, as a function's is.

// "A skill's name is lowercase letters, digits, and hyphens, starting with a
// letter; any other is refused on save and on install."
func TestASkillIsNamedInLowercaseWordsJoinedByHyphens(t *testing.T) {
	a := newTestApp(t)
	for _, name := range []string{"Voice_Over", "voice_over", "Voice-Over", "-voice", "voice-", "voice--over", "2voice"} {
		w := callAPI(t, a, "POST", "/skills", map[string]any{"name": name, "description": "Use when.", "body": "Steps."})
		if w.Code < 400 || !strings.Contains(w.Body.String(), "lowercase") {
			t.Errorf("saving %q = %d %s, want a refusal saying how a name is written", name, w.Code, w.Body.String())
		}
		if a.skills.Get(name) != nil {
			t.Errorf("%q was stored", name)
		}
	}
	for _, name := range []string{"voice-over", "v", "mp3-to-wav2"} {
		if w := callAPI(t, a, "POST", "/skills", map[string]any{"name": name, "description": "Use when.", "body": "Steps."}); w.Code != 200 {
			t.Errorf("saving %q = %d %s", name, w.Code, w.Body.String())
		}
	}

	s := newSession(t, a)
	path := draftSkill(t, a, s.ID, "voice_over", "---\nname: voice_over\ndescription: Use when.\n---\n", map[string]string{})
	if code, body := install(t, a, s.ID, path, false); code < 400 || !strings.Contains(body, "lowercase") {
		t.Errorf("installing voice_over = %d %s, want a refusal saying how a name is written", code, body)
	}
}

// "A skill on disk whose name is not written that way is skipped, and the
// failure says so."
func TestASkillOnDiskWithAnotherKindOfNameIsSkippedOutLoud(t *testing.T) {
	a := newTestApp(t)
	writeSkillIn(t, a.skills.UserDir(), "Voice_Over", "")
	a.skills.Load()
	if a.skills.Get("Voice_Over") != nil {
		t.Error("a skill named Voice_Over loaded")
	}
	found := false
	for _, f := range a.skills.Failures() {
		if strings.Contains(f.Reason, "lowercase") && strings.Contains(f.Dir, "Voice_Over") {
			found = true
		}
	}
	if !found {
		t.Errorf("failures = %+v, want one naming Voice_Over and how a name is written", a.skills.Failures())
	}
}

// "Every shipped tool's name is lowercase letters, digits, and underscores,
// starting with a letter."
func TestEveryShippedToolIsNamedLikeAFunction(t *testing.T) {
	named := regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
	dirs, err := os.ReadDir(filepath.Join("..", "..", "tools"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		raw, err := os.ReadFile(filepath.Join("..", "..", "tools", d.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var m struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		if !named.MatchString(m.Name) {
			t.Errorf("the tool %q is not named in lowercase words joined by underscores", m.Name)
		}
	}
}
