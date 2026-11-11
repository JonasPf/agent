package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Some skills are for a conversation that set out to use them: one that walks
// the agent through cloning and pushing code has no business in the index of a
// conversation about the weather. A skill says so in its own frontmatter, and a
// session that did not choose its skills gets every skill except those.

// writeSkill puts a skill in the root that ships with the image, which is where
// every skill came from before the operator could write one.
func writeSkill(t *testing.T, a *App, name, extra string) {
	t.Helper()
	writeSkillIn(t, a.skills.shipped, name, extra)
}

func writeSkillIn(t *testing.T, dir, name, extra string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: The " + name + " skill.\n" + extra + "---\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillsIndex(t *testing.T, a *App, s *Session) string {
	t.Helper()
	p, ok := a.promptEntry(s.ID)
	if !ok {
		t.Fatal("the session has no prompt")
	}
	for _, sec := range p.Sections {
		if sec.Name == "skills_index" {
			return sec.Text
		}
	}
	t.Fatal("the prompt has no skills index")
	return ""
}

func TestASkillSaysWhetherItIsOnByDefault(t *testing.T) {
	for _, c := range []struct {
		name  string
		extra string
		want  bool
	}{
		{"unsaid is on", "", true},
		{"on is on", "default: on\n", true},
		{"off is off", "default: off\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			sk, err := parseSkill("---\nname: x\ndescription: y\n" + c.extra + "---\nbody\n")
			if err != nil {
				t.Fatal(err)
			}
			if sk.DefaultEnabled != c.want {
				t.Errorf("DefaultEnabled = %v, want %v", sk.DefaultEnabled, c.want)
			}
		})
	}
}

// A default that is neither on nor off is a typo, and a typo in the one line
// that keeps a skill out of every conversation must not quietly put it in.
func TestAnUnreadableDefaultIsRefusedRatherThanGuessed(t *testing.T) {
	if _, err := parseSkill("---\nname: x\ndescription: y\ndefault: of\n---\nbody\n"); err == nil {
		t.Error("default: of was accepted; the skill would load with a default nobody wrote")
	}
}

func TestASkillThatShipsOffIsOutOfAConversationThatDidNotChooseIt(t *testing.T) {
	a := newTestApp(t)
	writeSkill(t, a, "everyday", "")
	writeSkill(t, a, "risky", "default: off\n")
	a.skills.Load()

	plain, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	idx := skillsIndex(t, a, plain)
	if !strings.Contains(idx, "everyday") {
		t.Errorf("a skill on by default is missing from the index: %q", idx)
	}
	if strings.Contains(idx, "risky") {
		t.Errorf("a skill off by default is in a conversation that did not choose it: %q", idx)
	}

	chose, err := a.NewSession(SessionConfig{Model: "test/model", EnabledSkills: []string{"everyday", "risky"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if idx := skillsIndex(t, a, chose); !strings.Contains(idx, "risky") {
		t.Errorf("a conversation that chose the skill does not have it: %q", idx)
	}
}

// The interface draws a new configuration from this list, so it has to say
// which skills start unticked as well as which a session holds.
func TestTheSkillListSaysWhichSkillsShipOff(t *testing.T) {
	a := newTestApp(t)
	writeSkill(t, a, "everyday", "")
	writeSkill(t, a, "risky", "default: off\n")
	a.skills.Load()
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/skills?session_id="+s.ID, nil))
	var got struct {
		Skills []struct {
			Name           string `json:"name"`
			DefaultEnabled bool   `json:"default_enabled"`
			Enabled        bool   `json:"enabled"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	seen := map[string]bool{}
	for _, sk := range got.Skills {
		seen[sk.Name] = true
		want := sk.Name != "risky"
		if sk.DefaultEnabled != want || sk.Enabled != want {
			t.Errorf("%s: default_enabled=%v enabled=%v, want both %v", sk.Name, sk.DefaultEnabled, sk.Enabled, want)
		}
	}
	if !seen["everyday"] || !seen["risky"] {
		t.Errorf("skills listed = %v, want everyday and risky", seen)
	}
}

// Only what most conversations need ships on (ADR-076). The rest is there for a
// conversation that chooses it.
func TestOnlyTheSkillsMostConversationsNeedShipOn(t *testing.T) {
	shipped := NewSkills(filepath.Join("..", "..", "skills"), "")
	if len(shipped.Failures()) > 0 {
		t.Fatalf("a shipped skill does not load: %+v", shipped.Failures())
	}
	want := map[string]bool{"researching": true,
		"keeping-records": false, "writing-skills": false, "voice-over": false, "video-edit": false}
	for _, sk := range shipped.All() {
		on, known := want[sk.Name]
		if !known {
			t.Errorf("%s ships, and nothing says whether it should be on", sk.Name)
			continue
		}
		if sk.DefaultEnabled != on {
			t.Errorf("%s: on by default is %v, want %v", sk.Name, sk.DefaultEnabled, on)
		}
	}
}

// The skills the spec requires are shipped and load.
func TestTheRequiredSkillsShip(t *testing.T) {
	shipped := NewSkills(filepath.Join("..", "..", "skills"), "")
	for _, name := range []string{"keeping-records", "researching", "writing-skills", "voice-over", "video-edit"} {
		if shipped.Get(name) == nil {
			t.Errorf("%s is not shipped", name)
		}
	}
}

// A conversation that chose nothing is indexed and can read only what ships on,
// and one that chose a skill that ships off has it.
func TestANewConversationHasOnlyTheSkillsThatShipOn(t *testing.T) {
	a := newTestApp(t)
	a.skills = NewSkills(filepath.Join("..", "..", "skills"), "")
	read := func(s *Session, name string) bool { return readsSkill(t, a, s, name) }
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	index, _ := promptSection(t, a, s, "skills_index")
	if !strings.Contains(index, "- researching: ") || !read(s, "researching") {
		t.Error("a new conversation does not have researching")
	}
	for _, name := range []string{"keeping-records", "writing-skills", "voice-over", "video-edit"} {
		if strings.Contains(index, "- "+name+": ") || read(s, name) {
			t.Errorf("a new conversation has %s", name)
		}
	}

	p, _ := a.promptEntry(s.ID)
	if !strings.Contains(systemMessage(p.Sections), "can fork this conversation with it on") {
		t.Error("the prompt sends a conversation without writing-skills to a skill it cannot read, with no way to get it")
	}

	chose, _ := a.NewSession(SessionConfig{Model: "test/model", EnabledSkills: []string{"voice-over"}}, "")
	if !read(chose, "voice-over") {
		t.Error("a conversation that chose a skill that ships off cannot read it")
	}
}

// A session's skills are fixed for its life, so a default that changes reaches
// the next conversation and leaves this one as its prompt says it is.
func TestADefaultThatChangesReachesOnlyLaterConversations(t *testing.T) {
	a := newTestApp(t)
	root := a.skills.UserDir()
	files := map[string]string{"notes.md": "Notes."}
	writeSkillFolder(t, root, "habit", "", files)
	writeSkillFolder(t, root, "rarely", "default: off\n", files)
	a.skills.Load()
	read := func(s *Session, name string) bool { return readsSkill(t, a, s, name) }
	reaches := func(s *Session, name string) bool {
		for _, dir := range a.skillFolders(s.ID) {
			if filepath.Base(dir) == name {
				return true
			}
		}
		return false
	}
	listed := func(s *Session, name string) bool {
		t.Helper()
		var out struct {
			Skills []struct {
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
			} `json:"skills"`
		}
		w := callAPI(t, a, "GET", "/skills?session_id="+s.ID, nil)
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		for _, sk := range out.Skills {
			if sk.Name == name {
				return sk.Enabled
			}
		}
		t.Fatalf("%s is not in the skill list", name)
		return false
	}

	started, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	writeSkillFolder(t, root, "habit", "default: off\n", files)
	writeSkillFolder(t, root, "rarely", "", files)
	a.skills.Load()
	later, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")

	for _, c := range []struct {
		who  *Session
		name string
		has  bool
	}{{started, "habit", true}, {started, "rarely", false}, {later, "habit", false}, {later, "rarely", true}} {
		if got := read(c.who, c.name); got != c.has {
			t.Errorf("reading %s = %v, want %v", c.name, got, c.has)
		}
		if reaches(c.who, c.name) != c.has {
			t.Errorf("a tool's access to %s's folder is not %v", c.name, c.has)
		}
		if listed(c.who, c.name) != c.has {
			t.Errorf("the skill list does not say %s is %v", c.name, c.has)
		}
	}
}
