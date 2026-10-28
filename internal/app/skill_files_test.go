package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A skill that needs more than one file is a folder: SKILL.md, whose
// frontmatter names it, and the files beside it. The index still carries one
// line for it, skill_read returns SKILL.md first and names the rest, and the
// interface shows and writes every file. These drive the real routes, and
// skill_read as the subprocess the agent runs.

// writeSkillFolder writes a skill as a folder into root: SKILL.md and files.
func writeSkillFolder(t *testing.T, root, name, extra string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	main := "---\nname: " + name + "\ndescription: The " + name + " skill.\n" + extra + "---\nRead packing.md first.\n"
	files["SKILL.md"] = main
	for path, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type skillReply struct {
	Name     string `json:"name"`
	Body     string `json:"body"`
	Source   string `json:"source"`
	Main     string `json:"main"`
	Editable bool   `json:"editable"`
	Files    []struct {
		Path  string `json:"path"`
		Body  string `json:"body"`
		Bytes int    `json:"bytes"`
	} `json:"files"`
}

func getSkill(t *testing.T, a *App, name string) skillReply {
	t.Helper()
	w := callAPI(t, a, "GET", "/skills/"+name, nil)
	if w.Code != 200 {
		t.Fatalf("GET /skills/%s: status %d (%s)", name, w.Code, w.Body.String())
	}
	var d skillReply
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return d
}

func filePaths(d skillReply) []string {
	var out []string
	for _, f := range d.Files {
		out = append(out, f.Path)
	}
	return out
}

func TestASkillFolderLoadsWithEveryFileInIt(t *testing.T) {
	a := newTestApp(t)
	writeSkillFolder(t, a.skills.shipped, "trip", "", map[string]string{
		"packing.md":        "# Packing\n\nChargers are one item per device.\n",
		"ref/checklist.csv": "item,always\npassport,yes\n",
		".DS_Store":         "\x00\x01",
	})
	a.skills.Load()

	d := getSkill(t, a, "trip")
	if d.Body != "Read packing.md first.\n" {
		t.Errorf("body = %q, want SKILL.md's body", d.Body)
	}
	if d.Main != "SKILL.md" {
		t.Errorf("main = %q, want SKILL.md", d.Main)
	}
	if !strings.HasPrefix(d.Source, "---\nname: trip\n") {
		t.Errorf("source = %q, want SKILL.md as it is on disk, front matter and all", d.Source)
	}
	if got := strings.Join(filePaths(d), " "); got != "packing.md ref/checklist.csv" {
		t.Errorf("files = %q, want the two beside SKILL.md, sorted, and no hidden file", got)
	}
	if d.Files[0].Body != "# Packing\n\nChargers are one item per device.\n" || d.Files[0].Bytes != len(d.Files[0].Body) {
		t.Errorf("packing.md = %+v, want its text and its size", d.Files[0])
	}

	w := callAPI(t, a, "GET", "/skills", nil)
	var list struct {
		Skills []struct {
			Name  string `json:"name"`
			Files int    `json:"files"`
		} `json:"skills"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Skills) != 1 || list.Skills[0].Files != 3 {
		t.Errorf("the list says %+v, want trip with 3 files", list.Skills)
	}

	// The index still carries one line, and none of the files.
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	idx := skillsIndex(t, a, s)
	if !strings.Contains(idx, "trip") || strings.Contains(idx, "Chargers") || strings.Contains(idx, "passport") {
		t.Errorf("index = %q, want trip's name and description and nothing from its files", idx)
	}
}

// A single file is still a skill of one file, and says which file it is.
func TestASkillOfOneFileHasNoOtherFiles(t *testing.T) {
	a := newTestApp(t)
	writeSkill(t, a, "watering", "")
	a.skills.Load()
	d := getSkill(t, a, "watering")
	if d.Main != "watering.md" || len(d.Files) != 0 {
		t.Errorf("main = %q, files = %v; want watering.md and nothing else", d.Main, filePaths(d))
	}
}

// skill_read hands the model files as text, so a file that is not text has no
// reading it could return. The skill is skipped and the screen says which file.
func TestASkillWithAFileThatIsNotTextIsSkippedAndSaysWhich(t *testing.T) {
	a := newTestApp(t)
	writeSkillFolder(t, a.skills.shipped, "trip", "", map[string]string{"map.png": "\x89PNG\r\n\x1a\n\xff\xfe"})
	a.skills.Load()
	if a.skills.Get("trip") != nil {
		t.Error("a skill with a file that is not text loaded")
	}
	f := a.skills.Failures()
	if len(f) != 1 || !strings.Contains(f[0].Reason, "map.png") {
		t.Errorf("failures = %+v, want one that names map.png", f)
	}
}

func TestAWrittenSkillWithFilesIsAFolderOnDisk(t *testing.T) {
	a := newTestApp(t)
	user := a.skills.UserDir()
	if w := callAPI(t, a, "POST", "/skills", map[string]any{
		"name": "watering", "description": "When to water.", "body": "See schedule.csv.",
		"files": []map[string]string{{"path": "schedule.csv", "body": "day,litres\nmon,2\n"}},
	}); w.Code != 200 {
		t.Fatalf("POST: status %d (%s)", w.Code, w.Body.String())
	}
	for _, p := range []string{"watering/SKILL.md", "watering/schedule.csv"} {
		if _, err := os.Stat(filepath.Join(user, p)); err != nil {
			t.Errorf("%s is not on disk: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(user, "watering.md")); !os.IsNotExist(err) {
		t.Errorf("a skill with files also left a single file: %v", err)
	}
	fresh := NewSkills(a.skills.shipped, user)
	if sk := fresh.Get("watering"); sk == nil || len(sk.Files) != 1 || sk.Files[0].Body != "day,litres\nmon,2\n" {
		t.Fatalf("a fresh load does not read the file back: %+v", sk)
	}

	// A PUT that does not mention the files keeps them: an interface that does
	// not know about files must not delete them by saving.
	if w := callAPI(t, a, "PUT", "/skills/watering", map[string]any{
		"description": "When to water.", "body": "Changed.",
	}); w.Code != 200 {
		t.Fatalf("PUT without files: status %d (%s)", w.Code, w.Body.String())
	}
	if d := getSkill(t, a, "watering"); strings.TrimSpace(d.Body) != "Changed." || len(d.Files) != 1 {
		t.Errorf("after a PUT without files: body %q, files %v; want the new body and the file kept", d.Body, filePaths(d))
	}

	// Renaming a file is the old path gone and the new one there.
	if w := callAPI(t, a, "PUT", "/skills/watering", map[string]any{
		"description": "When to water.", "body": "Changed.",
		"files": []map[string]string{{"path": "plan/week.csv", "body": "day,litres\n"}},
	}); w.Code != 200 {
		t.Fatalf("PUT with a renamed file: status %d (%s)", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(user, "watering", "schedule.csv")); !os.IsNotExist(err) {
		t.Errorf("the file's old name is still on disk: %v", err)
	}
	if d := getSkill(t, a, "watering"); strings.Join(filePaths(d), " ") != "plan/week.csv" {
		t.Errorf("files = %v, want plan/week.csv", filePaths(d))
	}

	// No files left is a skill of one file again.
	if w := callAPI(t, a, "PUT", "/skills/watering", map[string]any{
		"description": "When to water.", "body": "Changed.", "files": []any{},
	}); w.Code != 200 {
		t.Fatalf("PUT with no files: status %d (%s)", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(user, "watering")); !os.IsNotExist(err) {
		t.Errorf("the folder is still there with no files in it: %v", err)
	}
	if _, err := os.Stat(filepath.Join(user, "watering.md")); err != nil {
		t.Errorf("the skill is not a single file again: %v", err)
	}

	// And a folder is deleted whole.
	callAPI(t, a, "PUT", "/skills/watering", map[string]any{
		"description": "When to water.", "body": "Changed.",
		"files": []map[string]string{{"path": "a.md", "body": "A."}},
	})
	if w := callAPI(t, a, "DELETE", "/skills/watering", nil); w.Code != 204 {
		t.Fatalf("DELETE: status %d (%s)", w.Code, w.Body.String())
	}
	entries, _ := os.ReadDir(user)
	if len(entries) != 0 {
		t.Errorf("the operator's root still holds %d entries after the delete", len(entries))
	}
}

// A file's path is joined to the skill's folder, so one that could climb out
// of it, or take SKILL.md's place, would write where the operator did not ask.
func TestASkillFileWithAPathThatIsNotOneIsRefused(t *testing.T) {
	a := newTestApp(t)
	callAPI(t, a, "POST", "/skills", map[string]any{
		"name": "watering", "description": "When to water.", "body": "Twice a week.",
	})
	before, _ := os.ReadFile(filepath.Join(a.skills.UserDir(), "watering.md"))
	for _, files := range [][]map[string]string{
		{{"path": "../escape.md", "body": "x"}},
		{{"path": "/etc/escape.md", "body": "x"}},
		{{"path": "SKILL.md", "body": "x"}},
		{{"path": "skill.MD", "body": "x"}},
		{{"path": ".hidden", "body": "x"}},
		{{"path": "a//b.md", "body": "x"}},
		{{"path": "", "body": "x"}},
		{{"path": "empty.md", "body": "  \n"}},
		{{"path": "a.md", "body": "x"}, {"path": "a.md", "body": "y"}},
	} {
		w := callAPI(t, a, "PUT", "/skills/watering", map[string]any{
			"description": "When to water.", "body": "Changed.", "files": files,
		})
		if w.Code != 409 {
			t.Errorf("files %v: status %d, want 409", files, w.Code)
		}
	}
	after, _ := os.ReadFile(filepath.Join(a.skills.UserDir(), "watering.md"))
	if string(after) != string(before) {
		t.Error("a refused save changed what is on disk")
	}
	if _, err := os.Stat(filepath.Join(a.skills.UserDir(), "escape.md")); !os.IsNotExist(err) {
		t.Error("a path with .. wrote outside the skill's folder")
	}
}

// End to end: skill_read, run as the subprocess the agent runs, returns
// SKILL.md with the other files named after it, and any one of them by path.
func TestSkillReadReadsAFolderSkillAndEachOfItsFiles(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	a.cfg.ToolsDir = filepath.Join("..", "..", "tools")
	a.tools = NewRegistry(a.cfg.ToolsDir, DBPath(dir), a.store.DB())
	userSkills := t.TempDir()
	writeSkillFolder(t, userSkills, "trip", "", map[string]string{
		"packing.md": "Chargers are one item per device.\n",
	})
	writeSkillFolder(t, userSkills, "hidden", "default: off\n", map[string]string{
		"secret.md": "Not for this conversation.\n",
	})
	a.skills = NewSkills(filepath.Join("..", "..", "skills"), userSkills)
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %+v", failures)
	}
	toolAPI(t, a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	call := func(args string) toolResult {
		t.Helper()
		return a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "skill_read", json.RawMessage(args))
	}

	res := call(`{"name":"trip"}`)
	if !res.OK || !strings.Contains(res.Content, "---\nRead packing.md first.") || !strings.Contains(res.Content, "packing.md") ||
		!strings.Contains(res.Content, `"file"`) {
		t.Errorf("skill_read trip = %+v, want SKILL.md, then packing.md named with how to read it", res)
	}
	if res := call(`{"name":"trip","file":"packing.md"}`); !res.OK || res.Content != "Chargers are one item per device.\n" {
		t.Errorf("skill_read trip packing.md = %+v, want the file exactly as stored", res)
	}
	if res := call(`{"name":"trip","file":"nope.md"}`); res.OK || !strings.Contains(res.Error, "nope.md") || !strings.Contains(res.Error, "packing.md") {
		t.Errorf("skill_read of a file that is not there = %+v, want a failure naming it and the files there are", res)
	}
	if res := call(`{"name":"hidden","file":"secret.md"}`); res.OK {
		t.Errorf("skill_read read a file of a skill this conversation does not have: %+v", res)
	}
	if res := call(`{"name":"scheduling"}`); !res.OK || strings.Contains(res.Content, `"file"`) {
		t.Errorf("skill_read of a skill of one file = %+v, want its body and no list of files", res)
	}
}
