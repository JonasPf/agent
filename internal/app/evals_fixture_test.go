package app

import (
	"os"
	"path/filepath"
	"testing"
)

// A tool that operates on files cannot be evaluated without them: the edit
// case named a file nothing created, so it tested the model's willingness to
// hallucinate rather than the tool's description.
func TestEvalFilesAreSeededIntoTheCasesDirectory(t *testing.T) {
	ws := t.TempDir()
	if err := seedFiles(ws, map[string]string{
		"config.txt":     "mode = draft\n",
		"sub/nested.txt": "deeper\n",
	}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"config.txt": "mode = draft\n", "sub/nested.txt": "deeper\n"} {
		b, err := os.ReadFile(filepath.Join(ws, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(b) != want {
			t.Errorf("%s = %q, want %q", name, b, want)
		}
	}
}

func TestEvalFilesCannotClimbOutOfTheCasesDirectory(t *testing.T) {
	ws := t.TempDir()
	if err := seedFiles(ws, map[string]string{"../escaped.txt": "no"}); err == nil {
		t.Error("a case wrote outside its own working directory")
	}
	if _, err := os.Stat(filepath.Join(ws, "..", "escaped.txt")); err == nil {
		t.Error("the file was written despite the refusal")
	}
}

func TestNoFilesIsNotAnError(t *testing.T) {
	if err := seedFiles(t.TempDir(), nil); err != nil {
		t.Errorf("a case with no fixtures failed: %v", err)
	}
}

// A case about a skill that is not there tests the model's willingness to
// invent one. A case can bring the skills it is about, as the operator's own,
// for its turn only.
func TestEvalSkillsAreLoadedForTheCaseAndGoneAfter(t *testing.T) {
	a := newTestApp(t)
	done, err := a.seedSkills(map[string]string{
		"trip/SKILL.md":   "---\nname: trip\ndescription: Use when a trip is planned.\n---\nRead packing.md.\n",
		"trip/packing.md": "- chargers\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	sk := a.skills.Get("trip")
	if sk == nil || len(sk.Files) != 1 || sk.Files[0].Path != "packing.md" {
		t.Fatalf("the case's skill did not load with its file: %+v", sk)
	}
	done()
	if a.skills.Get("trip") != nil {
		t.Error("the case's skill outlived the case")
	}
	if _, err := a.seedSkills(map[string]string{"../escaped.md": "no"}); err == nil {
		t.Error("a case wrote a skill outside the skills root")
	}
}
