package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Where Landlock confines a tool, the registry runs it as
// `<this binary> -confine <policy> -- <tool>`: the running program is its own
// sandbox wrapper, whichever program that is. The agent answered that second
// run and this command did not, so on Linux every tool an eval case called died
// on an unknown flag, and the case read as one the model got wrong. On a laptop
// nothing is confined, nothing is wrapped, and the evals looked fine.
//
// The binary is built and run as the registry runs it. With nothing after --,
// Confine refuses before it asks the kernel for anything, so the answer is the
// same on every platform: an error from Confine, not from the flag parser.
func TestEvalAnswersTheSandboxWrapperAsTheAgentDoes(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "eval")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, _ := exec.Command(bin, "-confine", "{}", "--").CombinedOutput()
	if strings.Contains(string(out), "flag provided but not defined") {
		t.Fatalf("eval does not know -confine, so no confined tool can run under it:\n%s", out)
	}
	if !strings.Contains(string(out), "confine: nothing to run") {
		t.Errorf("eval -confine did not reach Confine:\n%s", out)
	}
}
