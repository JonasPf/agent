package app

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// A conversation drafts a skill in its own working directory, and the operator
// installs it from there (ADR-071). Installing is the approval: nothing the
// agent writes reaches a skill folder any other way. It is checked as a save
// is, a skill of the operator's is replaced only when the request says so, and
// a name that ships stays the shipped skill's.

// skillFolders is where the skills a conversation has keep their files: what
// its tools may read, and so run, and never write. A skill of one file has no
// folder; its text is what skill_read returns.
func (a *App) skillFolders(sessionID string) []string {
	sess := a.store.Session(sessionID)
	if sess == nil {
		return nil
	}
	var dirs []string
	for _, sk := range a.skills.All() {
		if sk.Main() == skillMain && sess.skillEnabled(sk) {
			dirs = append(dirs, absOr(filepath.Dir(sk.Path)))
		}
	}
	return dirs
}

func (a *App) hInstallSkill(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Path      string `json:"path"`
		Replace   bool   `json:"replace"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if a.store.Session(in.SessionID) == nil {
		fail(w, 404, "no such session")
		return
	}
	sk, files, err := a.draftedSkill(in.SessionID, in.Path)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if existing := a.skills.Get(sk.Name); existing != nil {
		if !existing.Editable {
			fail(w, 409, "a skill named %q ships with the agent; rename the draft", sk.Name)
			return
		}
		if !in.Replace {
			fail(w, 409, "you already have a skill named %q; installing this replaces it", sk.Name)
			return
		}
	}
	if err := a.skills.Save(sk.Name, sk.Description, sk.Body, sk.DefaultEnabled, files); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	a.hub.Broadcast(wsEvent{Kind: "skills"})
	saved := a.skills.Get(sk.Name)
	if saved == nil {
		fail(w, 500, "the skill was written but does not load")
		return
	}
	writeJSON(w, 201, skillDoc(saved))
}

// draftedSkill reads a skill folder from a conversation's working directory:
// SKILL.md, parsed as the skill, and every other file in the folder. It reads
// through the directory's root, so a path that climbs out or a link that
// leads out names nothing. Hidden files are left out, as they are when a
// skill folder is loaded; a file that is not text refuses the whole folder.
func (a *App) draftedSkill(sessionID, dir string) (*Skill, []SkillFile, error) {
	rel, err := workspacePath(sessionID, dir)
	if err != nil {
		return nil, nil, err
	}
	root, err := a.workspaceRoot(sessionID)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	main, err := root.ReadFile(path.Join(rel, skillMain))
	if err != nil {
		return nil, nil, fmt.Errorf("%s holds no %s in this conversation's directory", rel, skillMain)
	}
	sk, err := parseSkill(string(main))
	if err != nil {
		return nil, nil, fmt.Errorf("%s/%s: %v (it opens with front matter: ---, name:, description:, ---)", rel, skillMain, err)
	}
	if err := validSkillName(sk.Name); err != nil {
		return nil, nil, fmt.Errorf("%s/%s: %v", rel, skillMain, err)
	}
	if err := checkAuthored(sk.Name, sk.Description, sk.Body); err != nil {
		return nil, nil, fmt.Errorf("%s/%s: %v", rel, skillMain, err)
	}
	var files []SkillFile
	err = fs.WalkDir(root.FS(), rel, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != rel && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		name := strings.TrimPrefix(p, rel+"/")
		if name == skillMain {
			return nil
		}
		b, err := root.ReadFile(p)
		if err != nil {
			return err
		}
		if !utf8.Valid(b) {
			return fmt.Errorf("%s is not text; a skill holds text only, so say what it needs instead of bundling it", name)
		}
		files = append(files, SkillFile{Path: name, Body: string(b), Bytes: len(b)})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return sk, files, nil
}
