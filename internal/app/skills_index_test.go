package app

import (
	"strings"
	"testing"
)

// A skill was read in 40 of the 132 turns that added to a record it covers,
// and in 13 of 46 that researched on the web. The index listed what each skill
// was and said nothing about when to read one; the one rule that did sat in the
// middle of the scheduling rules, and nothing said that compaction folds a
// skill's text away like any other turn. So the index says it, above the list.
func TestTheSkillsIndexSaysWhenToReadASkill(t *testing.T) {
	a := newTestApp(t)
	writeSkill(t, a, "watering", "")
	a.skills.Load()
	s := newSession(t, a)

	idx := skillsIndex(t, a, s)
	head, _, _ := strings.Cut(idx, "\n- ")
	for _, want := range []string{"skill_read", "before", "compaction"} {
		if !strings.Contains(head, want) {
			t.Errorf("the lead-in above the skills does not say %q:\n%s", want, idx)
		}
	}
	if !strings.Contains(idx, "- watering: The watering skill.") {
		t.Errorf("the index does not list the skill:\n%s", idx)
	}
}

// With nothing to read, there is nothing to say about reading it.
func TestAnEmptySkillsIndexHasNoLeadIn(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	if idx := skillsIndex(t, a, s); idx != "(none)" {
		t.Errorf("a session with no skills is told %q, want (none)", idx)
	}
}

// Every shipped description says when to use the skill, since that is the one
// line the model decides from.
func TestEveryShippedSkillSaysWhenToUseIt(t *testing.T) {
	shipped := NewSkills("../../skills", "")
	for _, sk := range shipped.All() {
		if !strings.HasPrefix(sk.Description, "Use when ") && !strings.HasPrefix(sk.Description, "Use before ") {
			t.Errorf("%s is described as %q; start with when to use it", sk.Name, sk.Description)
		}
	}
}
