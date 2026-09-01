package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Bytes       int    `json:"bytes"`
	Body        string `json:"-"`
	Path        string `json:"-"`
}

type Skills struct {
	dir      string
	mu       sync.RWMutex
	skills   map[string]*Skill
	failures []LoadFailure
}

func NewSkills(dir string) *Skills {
	s := &Skills{dir: dir, skills: map[string]*Skill{}}
	s.Load()
	return s
}

// Load reads every skill file. A skill with malformed frontmatter is skipped
// and the failure stays visible.
func (s *Skills) Load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skills = map[string]*Skill{}
	s.failures = nil
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(s.dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			s.failures = append(s.failures, LoadFailure{Dir: e.Name(), Reason: err.Error()})
			continue
		}
		sk, err := parseSkill(string(b))
		if err != nil {
			s.failures = append(s.failures, LoadFailure{Dir: e.Name(), Reason: err.Error()})
			continue
		}
		sk.Path = path
		sk.Bytes = len(b)
		s.skills[sk.Name] = sk
	}
}

func parseSkill(text string) (*Skill, error) {
	if !strings.HasPrefix(text, "---") {
		return nil, errFrontmatter
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, errFrontmatter
	}
	head, body := rest[:end], strings.TrimPrefix(rest[end+4:], "\n")
	sk := &Skill{Body: body}
	for _, line := range strings.Split(head, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "name":
			sk.Name = strings.TrimSpace(v)
		case "description":
			sk.Description = strings.TrimSpace(v)
		}
	}
	if sk.Name == "" || sk.Description == "" {
		return nil, errFrontmatter
	}
	return sk, nil
}

type frontmatterError struct{}

func (frontmatterError) Error() string { return "malformed frontmatter: needs name and description" }

var errFrontmatter = frontmatterError{}

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
