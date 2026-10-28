package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The agent drafts a skill in its own working directory and the operator
// installs it from there (ADR-071). Installing is the approval: it is checked
// as a save is, it does not replace a skill of the operator's without being
// told to, and a name that ships stays the shipped skill's.

// draftSkill writes a skill folder into a conversation's working directory, at
// skills/<name>/, with the given front matter and files.
func draftSkill(t *testing.T, a *App, sessionID, name, frontMatter string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(a.sessionWorkspace(sessionID), "skills", name)
	files["SKILL.md"] = frontMatter + "Extract the audio, transcribe it, then speak it back.\n"
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return "skills/" + name
}

func voiceOverFront(description string) string {
	return "---\nname: voice-over\ndescription: " + description + "\n---\n"
}

func install(t *testing.T, a *App, sessionID, path string, replace bool) (int, string) {
	t.Helper()
	w := callAPI(t, a, "POST", "/skills/install", map[string]any{"session_id": sessionID, "path": path, "replace": replace})
	return w.Code, w.Body.String()
}

// "A folder in a conversation's working directory holding SKILL.md is
// installed as a skill through the API, named by its front matter, with every
// other file in the folder as the skill's."
func TestADraftedSkillInstallsFromTheConversation(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	path := draftSkill(t, a, s.ID, "voice-over", voiceOverFront("Use when a video needs a new voice."), map[string]string{
		"scripts/mux.sh":                      "ffmpeg -i in.mp4 -i voice.wav out.mp4\n",
		"evals/short-clip/prompt.md":          "Make a voiceover for clip.mov\n",
		"evals/short-clip/graders/01-file.md": "---\ntype: llm\nfocus: trace\nweight: 1\n---\n\nvoiceover.mp4 exists\n",
	})
	code, body := install(t, a, s.ID, path, false)
	if code != 201 {
		t.Fatalf("install = %d %s", code, body)
	}
	got := getSkill(t, a, "voice-over")
	if !got.Editable || !strings.Contains(got.Body, "transcribe it") {
		t.Errorf("installed skill = %+v", got)
	}
	want := "evals/short-clip/graders/01-file.md evals/short-clip/prompt.md scripts/mux.sh"
	if paths := strings.Join(filePaths(got), " "); paths != want {
		t.Errorf("files = %s, want %s", paths, want)
	}
	// It is the operator's skill now, on disk outside the conversation, so it
	// outlives the conversation that drafted it.
	if err := os.RemoveAll(a.sessionWorkspace(s.ID)); err != nil {
		t.Fatal(err)
	}
	a.skills.Load()
	if a.skills.Get("voice-over") == nil {
		t.Error("the installed skill went with the conversation's directory")
	}
}

// "Installing over a skill the operator already has is refused unless the
// request says to replace it, and the refusal names that skill; a name that
// ships is refused either way."
func TestInstallingOverASkillAsksFirst(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	path := draftSkill(t, a, s.ID, "voice-over", voiceOverFront("Use when a video needs a new voice."), map[string]string{})
	if code, body := install(t, a, s.ID, path, false); code != 201 {
		t.Fatalf("first install = %d %s", code, body)
	}
	draftSkill(t, a, s.ID, "voice-over", voiceOverFront("Use when a video needs a new voice, read by call_model."), map[string]string{})

	code, body := install(t, a, s.ID, path, false)
	if code != 409 || !strings.Contains(body, "voice-over") {
		t.Errorf("installing over an existing skill = %d %s, want 409 naming it", code, body)
	}
	if got := getSkill(t, a, "voice-over"); strings.Contains(got.Source, "call_model") {
		t.Error("the refused install replaced the skill anyway")
	}
	if code, body := install(t, a, s.ID, path, true); code != 201 {
		t.Fatalf("install with replace = %d %s", code, body)
	}
	if got := getSkill(t, a, "voice-over"); !strings.Contains(got.Source, "call_model") {
		t.Errorf("replace kept the old skill: %q", got.Source)
	}

	writeSkill(t, a, "scheduling", "")
	a.skills.Load()
	shipped := filepath.Join(a.skills.shipped, "scheduling.md")
	path = draftSkill(t, a, s.ID, "scheduling", "---\nname: scheduling\ndescription: Use when a job is wanted.\n---\n", map[string]string{})
	for _, replace := range []bool{false, true} {
		if code, body := install(t, a, s.ID, path, replace); code < 400 || !strings.Contains(body, "ships") {
			t.Errorf("installing over a shipped skill (replace %v) = %d %s, want a refusal saying it ships", replace, code, body)
		}
	}
	if got, _ := os.ReadFile(shipped); !strings.Contains(string(got), "The scheduling skill.") {
		t.Error("the shipped skill was touched")
	}
}

// "A folder that would be refused as a save is refused as an install, says
// why, and nothing is stored." And a path that is not a skill folder of this
// conversation's is refused before anything is read.
func TestAnInstallThatWouldNotSaveIsRefused(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	other := newSession(t, a)
	noFront := draftSkill(t, a, s.ID, "bare", "", map[string]string{})
	twoLines := draftSkill(t, a, s.ID, "wordy", "---\nname: wordy\ndescription: |\n  one\n  two\n---\n", map[string]string{})
	binary := draftSkill(t, a, s.ID, "binary", "---\nname: binary\ndescription: Use when.\n---\n",
		map[string]string{"bin/ffmpeg": "\x7fELF\x02\x01\x01\x00\xb8\xff\xfe"})
	elsewhere := draftSkill(t, a, other.ID, "theirs", "---\nname: theirs\ndescription: Use when.\n---\n", map[string]string{})
	if err := os.MkdirAll(filepath.Join(a.sessionWorkspace(s.ID), "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, says string }{
		{noFront, "front"},
		{twoLines, "one line"},
		{binary, "bin/ffmpeg"},
		{"empty", "SKILL.md"},
		{"../" + other.ID + "/" + elsewhere, "SKILL.md"},
	} {
		code, body := install(t, a, s.ID, c.path, false)
		if code < 400 || !strings.Contains(body, c.says) {
			t.Errorf("install %s = %d %s, want a refusal mentioning %q", c.path, code, body, c.says)
		}
	}
	for _, name := range []string{"bare", "wordy", "binary", "theirs"} {
		if a.skills.Get(name) != nil {
			t.Errorf("%s was stored although its install was refused", name)
		}
	}
}

// "A tool can read and run the files of a skill folder its conversation has,
// cannot write them, and cannot read a skill its conversation does not have."
func TestAConversationsToolsRunTheSkillsItHas(t *testing.T) {
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	copyToolTree(t, "../../tools", toolsDir)
	userSkills := filepath.Join(root, "user-skills")
	writeSkillFolder(t, userSkills, "runner", "", map[string]string{
		"scripts/hello.sh": "echo hello from a skill\n",
	})
	writeSkillFolder(t, userSkills, "hidden", "default: off\n", map[string]string{
		"secret.md": "not this conversation's\n",
	})
	a, _ := confinedApp(t, toolsDir, filepath.Join(root, "skills"), "")
	a.skills = NewSkills(filepath.Join(root, "skills"), userSkills)
	confinementOrSkip(t, a.sandbox)
	run := shellIn(t, a)

	runner := filepath.Join(userSkills, "runner")
	if res := run("sh " + runner + "/scripts/hello.sh"); !res.OK || !strings.Contains(res.Content, "hello from a skill") {
		t.Errorf("running a skill's script = %+v", res)
	}
	if res := run("echo changed > " + runner + "/scripts/hello.sh"); res.OK {
		t.Errorf("a tool wrote into a skill: %q", res.Content)
	}
	if got, _ := os.ReadFile(filepath.Join(runner, "scripts", "hello.sh")); string(got) != "echo hello from a skill\n" {
		t.Errorf("the skill's script was changed: %q", got)
	}
	if res := run("cat " + filepath.Join(userSkills, "hidden", "secret.md")); res.OK && strings.Contains(res.Content, "not this") {
		t.Errorf("a tool read a skill its conversation does not have: %q", res.Content)
	}
}

// skill_read returns the main file as stored, front matter and all, so every
// skill is an example of the format; and a folder skill says where its files
// are, so a script in it can be run.
func TestSkillReadReturnsTheFileAsStoredAndWhereItLives(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	a.cfg.ToolsDir = filepath.Join("..", "..", "tools")
	a.tools = NewRegistry(a.cfg.ToolsDir, DBPath(dir), a.store.DB())
	userSkills := t.TempDir()
	writeSkillFolder(t, userSkills, "trip", "", map[string]string{"packing.md": "Chargers.\n"})
	a.skills = NewSkills(filepath.Join("..", "..", "skills"), userSkills)
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %+v", failures)
	}
	toolAPI(t, a)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	call := func(args string) toolResult {
		t.Helper()
		return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "skill_read", json.RawMessage(args))
	}
	if res := call(`{"name":"scheduling"}`); !res.OK || !strings.HasPrefix(res.Content, "---\nname: scheduling\ndescription: ") {
		t.Errorf("skill_read scheduling = %q, want the file with its front matter", res.Content)
	}
	res := call(`{"name":"trip"}`)
	if !res.OK || !strings.HasPrefix(res.Content, "---\nname: trip\n") || !strings.Contains(res.Content, filepath.Join(userSkills, "trip")) {
		t.Errorf("skill_read trip = %q, want SKILL.md as stored and the folder's path", res.Content)
	}
}
