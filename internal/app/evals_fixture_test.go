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
