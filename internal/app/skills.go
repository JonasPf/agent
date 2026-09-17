package app

import (
	"fmt"
	"sort"
	"sync"
)

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Bytes       int    `json:"bytes"`
	// DefaultEnabled is whether a session that did not choose its skills has
	// this one. A skill turns it off with `default: off` in its frontmatter,
	// which is for a skill a conversation should have set out to use.
	DefaultEnabled bool `json:"default_enabled"`
	// Editable is whether this skill is one the operator wrote through the
	// interface. A skill that ships in the image is not: replacing the image
	// would replace it, so an edit that appeared to work would be lost on the
	// next deployment.
	Editable bool   `json:"editable"`
	Body     string `json:"-"`
	Path     string `json:"-"`
}

type Skills struct {
	// shipped ships in the image and is read-only. user is beside the data and
	// is what the interface writes.
	shipped  string
	user     string
	mu       sync.RWMutex
	skills   map[string]*Skill
	failures []LoadFailure
}

func NewSkills(shipped, user string) *Skills {
	s := &Skills{shipped: shipped, user: user, skills: map[string]*Skill{}}
	s.Load()
	return s
}

// UserDir is the root the operator's skills are written to.
func (s *Skills) UserDir() string { return s.user }

// Load reads every skill file from both roots. A skill with malformed
// frontmatter is skipped and the failure stays visible.
func (s *Skills) Load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skills = map[string]*Skill{}
	s.failures = nil

	shipped, failures := loadAuthoredDir(s.shipped, false)
	s.failures = append(s.failures, failures...)
	user, failures := loadAuthoredDir(s.user, true)
	s.failures = append(s.failures, failures...)

	for _, d := range append(shipped, user...) {
		sk, err := skillFrom(d)
		if err != nil {
			s.failures = append(s.failures, LoadFailure{Dir: d.Path, Reason: err.Error()})
			continue
		}
		// A name is an identifier, and two skills answering to one would make
		// the index a lie about what skill_read returns. The shipped one is
		// loaded first and keeps the name; the operator's is refused out loud,
		// rather than shadowing something the agent was built with.
		if existing, ok := s.skills[sk.Name]; ok && !existing.Editable {
			s.failures = append(s.failures, LoadFailure{Dir: d.Path,
				Reason: fmt.Sprintf("a skill named %q ships with the agent; choose another name", sk.Name)})
			continue
		}
		s.skills[sk.Name] = sk
	}
}

// skillFrom reads the one field a skill has beyond a name and a description.
func skillFrom(d *authored) (*Skill, error) {
	sk := &Skill{Name: d.Name, Description: d.Description, Body: d.Body,
		Bytes: d.Bytes, Path: d.Path, Editable: d.Editable, DefaultEnabled: true}
	// Anything but on or off is refused rather than read as one of them: a typo
	// here would otherwise put a skill meant to be off into every conversation,
	// and nothing on screen would say why.
	switch v, ok := d.Fields["default"]; {
	case !ok, v == "on":
		sk.DefaultEnabled = true
	case v == "off":
		sk.DefaultEnabled = false
	default:
		return nil, fmt.Errorf("malformed frontmatter: default must be on or off, not %q", v)
	}
	return sk, nil
}

// parseSkill reads one skill from the text of its file.
func parseSkill(text string) (*Skill, error) {
	fields, body, err := parseFrontmatter(text)
	if err != nil {
		return nil, err
	}
	if fields["name"] == "" || fields["description"] == "" {
		return nil, errFrontmatter
	}
	return skillFrom(&authored{Name: fields["name"], Description: fields["description"],
		Body: body, Fields: fields, Bytes: len(text)})
}

func (s *Skills) Get(name string) *Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.skills[name]
}

func (s *Skills) All() []*Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Skill, 0, len(s.skills))
	for _, v := range s.skills {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Skills) Failures() []LoadFailure {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]LoadFailure(nil), s.failures...)
}

// Save writes one of the operator's skills and reloads. A skill that ships in
// the image is refused: the name belongs to the image, and a copy in the
// writable root would shadow it until the next deployment brought the original
// back.
//
// A saved skill is not in the prompt of any session already running. It reaches
// the sessions started after it, the way a written memory does.
func (s *Skills) Save(name, description, body string, defaultEnabled bool) error {
	if existing := s.Get(name); existing != nil && !existing.Editable {
		return fmt.Errorf("%q ships with the agent and cannot be edited here", name)
	}
	on := "on"
	if !defaultEnabled {
		on = "off"
	}
	if err := saveAuthored(s.user, name, description, map[string]string{"default": on}, body); err != nil {
		return err
	}
	s.Load()
	return nil
}

// Delete removes one of the operator's skills and reloads.
func (s *Skills) Delete(name string) error {
	existing := s.Get(name)
	if existing == nil {
		return fmt.Errorf("no skill named %q", name)
	}
	if !existing.Editable {
		return fmt.Errorf("%q ships with the agent and cannot be deleted here", name)
	}
	if err := deleteAuthored(s.user, name); err != nil {
		return err
	}
	s.Load()
	return nil
}
