package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The agent proposes changes to itself, and there is no code for it: a skill
// says how, `git` and `gh` are in the image, and a credential reaches whichever
// tool names it. That the capability is composition rather than a feature is a
// property worth keeping, and the way to keep it is to notice when it stops
// being true.
//
// So: no source file may name the credential or the repository. A change that
// needs one is a change to the architecture, and it should have to say so here
// before it says so in the code.
func TestNothingInTheAgentKnowsItCanChangeItself(t *testing.T) {
	roots := []string{"..", "../../cmd", "../../tools"}
	forbidden := []string{"GH_TOKEN", "AGENT_REPO", "changing-yourself"}

	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			// A test may name the credential: the environment allow-list is
			// tested with it, because it is the case the allow-list exists for.
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, word := range forbidden {
				if strings.Contains(string(b), word) {
					t.Errorf("%s names %q.\n"+
						"The agent has no self-modification code: the capability is a skill, git and gh in\n"+
						"the image, and a credential delivered through a tool manifest. If that is changing,\n"+
						"change specs/architecture.html first — see \"Changing the agent\".", path, word)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The other half of the same property: what the skill needs is prose and two
// programs, so the skill has to exist and has to say what it needs.
func TestTheProcedureIsASkillAndNothingElse(t *testing.T) {
	body, err := os.ReadFile("../../skills/changing-yourself.md")
	if err != nil {
		t.Fatalf("the procedure is missing: %v", err)
	}
	for _, needed := range []string{"git clone", "gh pr create", "$AGENT_REPO"} {
		if !strings.Contains(string(body), needed) {
			t.Errorf("the skill does not mention %q, and nothing else tells the agent how", needed)
		}
	}
	// The one thing the agent must not be led to believe it can do.
	for _, absent := range []string{"gh pr merge", "--admin"} {
		if strings.Contains(string(body), absent) {
			t.Errorf("the skill mentions %q; merging is the reviewer's, not the agent's", absent)
		}
	}
}
