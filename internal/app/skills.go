package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// skillMain is the file that makes a folder a skill. A skill that needs more
// than one file is a folder holding it and the files beside it; one that needs
// a single file can still be just <name>.md.
const skillMain = "SKILL.md"

// SkillFile is one file beside SKILL.md, by its path inside the skill's folder.
// The agent reads one with skill_read; nothing in the index names it.
type SkillFile struct {
	Path  string `json:"path"`
	Body  string `json:"body"`
	Bytes int    `json:"bytes"`
}

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
	// Source is the main file as written, and Files the others beside it.
	Source string      `json:"-"`
	Files  []SkillFile `json:"-"`
}

// Main is the name of the file the skill's front matter and body are in.
func (sk *Skill) Main() string { return filepath.Base(sk.Path) }

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

	var docs []*authored
	for _, root := range []struct {
		dir      string
		editable bool
	}{{s.shipped, false}, {s.user, true}} {
		files, failures := loadAuthoredDir(root.dir, root.editable)
		s.failures = append(s.failures, failures...)
		folders, failures := loadSkillFolders(root.dir, root.editable)
		s.failures = append(s.failures, failures...)
		docs = append(docs, append(files, folders...)...)
	}

	for _, d := range docs {
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
		Bytes: d.Bytes, Path: d.Path, Editable: d.Editable, DefaultEnabled: true,
		Source: d.Source, Files: d.Files}
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

// loadSkillFolders reads every skill in root that is a folder. A folder with no
// SKILL.md, or with a file that is not text, is a failure on screen rather than
// a skill that is quietly missing, or one whose file skill_read cannot return.
func loadSkillFolders(root string, editable bool) ([]*authored, []LoadFailure) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	var docs []*authored
	var failures []LoadFailure
	for _, e := range entries {
		// A hidden folder is not a skill: it is where a save is put together
		// before it replaces the one it changes.
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		where := e.Name() + "/" + skillMain
		d, err := readAuthored(filepath.Join(root, e.Name(), skillMain), editable)
		if err != nil {
			if os.IsNotExist(err) {
				err = fmt.Errorf("a skill that is a folder needs a %s", skillMain)
			}
			failures = append(failures, LoadFailure{Dir: where, Reason: err.Error()})
			continue
		}
		if d.Files, err = readSkillFiles(filepath.Join(root, e.Name())); err != nil {
			failures = append(failures, LoadFailure{Dir: where, Reason: err.Error()})
			continue
		}
		for _, f := range d.Files {
			d.Bytes += f.Bytes
		}
		docs = append(docs, d)
	}
	return docs, failures
}

// readSkillFiles reads the files beside SKILL.md, sorted by path. Hidden files
// are left out — a Finder's .DS_Store is not part of anything the operator
// wrote.
func readSkillFiles(dir string) ([]SkillFile, error) {
	var files []SkillFile
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if rel == skillMain {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(b) {
			return fmt.Errorf("%s is not text, and skill_read returns a skill's files as text", rel)
		}
		files = append(files, SkillFile{Path: rel, Body: string(b), Bytes: len(b)})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

// checkSkillFiles refuses a set of files that could not be written as given. A
// path is joined to the skill's folder, so one that could climb out of it, or
// take SKILL.md's place, would write somewhere the operator did not ask.
func checkSkillFiles(files []SkillFile) error {
	seen := map[string]bool{}
	for _, f := range files {
		if f.Path == "" {
			return fmt.Errorf("a file needs a path")
		}
		if len(f.Path) > 200 {
			return fmt.Errorf("a file's path is at most 200 characters")
		}
		for _, part := range strings.Split(f.Path, "/") {
			if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
				return fmt.Errorf("%q is not a path inside the skill: each part is a name, and none starts with a dot", f.Path)
			}
			for _, r := range part {
				if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_' || r == '.') {
					return fmt.Errorf("a file's path holds letters, digits, dots, hyphens, underscores, and slashes; %q does not", f.Path)
				}
			}
		}
		if strings.EqualFold(f.Path, skillMain) {
			return fmt.Errorf("%s is the skill's main file; its text is the body", skillMain)
		}
		if seen[f.Path] {
			return fmt.Errorf("there are two files at %s", f.Path)
		}
		seen[f.Path] = true
		if strings.TrimSpace(f.Body) == "" {
			return fmt.Errorf("%s is empty", f.Path)
		}
	}
	return nil
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
//
// A skill with files is written as a folder, and one without as a single file;
// whichever form it had before is removed, so a skill is only ever on disk once.
func (s *Skills) Save(name, description, body string, defaultEnabled bool, files []SkillFile) error {
	if existing := s.Get(name); existing != nil && !existing.Editable {
		return fmt.Errorf("%q ships with the agent and cannot be edited here", name)
	}
	if err := checkAuthored(name, description, body); err != nil {
		return err
	}
	if err := checkSkillFiles(files); err != nil {
		return err
	}
	on := "on"
	if !defaultEnabled {
		on = "off"
	}
	fields := map[string]string{"default": on}
	single, folder := filepath.Join(s.user, name+".md"), filepath.Join(s.user, name)
	if len(files) == 0 {
		if err := saveAuthored(s.user, name, description, fields, body); err != nil {
			return err
		}
		if err := os.RemoveAll(folder); err != nil {
			return err
		}
	} else {
		if err := replaceSkillFolder(s.user, name, renderAuthored(name, description, fields, body), files); err != nil {
			return err
		}
		if err := os.Remove(single); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.Load()
	return nil
}

// replaceSkillFolder puts a skill's folder together beside the one it replaces,
// then swaps it in, so a save that fails part way leaves the old one whole.
func replaceSkillFolder(root, name string, main []byte, files []SkillFile) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(root, "."+name+".")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	write := func(rel string, b []byte) error {
		p := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, b, 0o644)
	}
	if err := write(skillMain, main); err != nil {
		return err
	}
	for _, f := range files {
		if err := write(f.Path, []byte(f.Body)); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	folder := filepath.Join(root, name)
	old := tmp + ".old"
	if err := os.Rename(folder, old); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmp, folder); err != nil {
		os.Rename(old, folder)
		return err
	}
	return os.RemoveAll(old)
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
	// By where it was loaded from: a folder goes whole.
	var err error
	if existing.Main() == skillMain {
		err = os.RemoveAll(filepath.Dir(existing.Path))
	} else {
		err = os.Remove(existing.Path)
	}
	if err != nil {
		return err
	}
	s.Load()
	return nil
}
