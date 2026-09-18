package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Some skills are for a conversation that set out to use them. changing-yourself
// is the first: it walks the agent through cloning and pushing its own code, and
// a conversation about the weather has no business being told how. A skill says
// so in its own frontmatter, and a session that did not choose its skills gets
// every skill except those.

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

// The criterion as shipped: a skill about changing code is off unless a
// conversation turns it on — the agent's own code or anyone else's. Both walk
// the agent through cloning and pushing, both want a credential a conversation
// has to be granted, and neither belongs in the index of a conversation about
// the weather. Every other shipped skill is on.
func TestTheSkillsThatChangeCodeShipOff(t *testing.T) {
	shipped := NewSkills(filepath.Join("..", "..", "skills"), "")
	if len(shipped.Failures()) > 0 {
		t.Fatalf("a shipped skill does not load: %+v", shipped.Failures())
	}
	off := map[string]bool{"changing-yourself": true, "working-on-a-repository": true}
	for name := range off {
		sk := shipped.Get(name)
		if sk == nil {
			t.Fatalf("%s is not shipped", name)
		}
		if sk.DefaultEnabled {
			t.Errorf("%s is on in every conversation that did not choose its skills", name)
		}
	}
	for _, other := range shipped.All() {
		if !off[other.Name] && !other.DefaultEnabled {
			t.Errorf("%s ships off; only the skills that change code should", other.Name)
		}
	}
}

// The skills the spec requires are shipped, load, and are in the index of a
// conversation that did not choose its skills.
func TestTheRequiredSkillsShipOn(t *testing.T) {
	shipped := NewSkills(filepath.Join("..", "..", "skills"), "")
	for _, name := range []string{"scheduling", "keeping-records", "researching"} {
		sk := shipped.Get(name)
		if sk == nil {
			t.Errorf("%s is not shipped", name)
			continue
		}
		if !sk.DefaultEnabled {
			t.Errorf("%s ships off", name)
		}
	}
}
