package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Settings arrive in the environment, and the only place an operator learns
// which exist is .env.example. A variable added to the code and not to that file
// is a setting nobody can find; one written there and read by nothing is a
// setting that silently does nothing. Both have happened — AGENT_EVAL_MODEL was
// undocumented for as long as it existed, and the README carried
// AGENT_ROTATE_TOKENS for weeks after compaction replaced rotation.
//
// So the file is checked against the source rather than maintained by memory.

var envRead = regexp.MustCompile(`(?:envOr|envInt|os\.Getenv|os\.LookupEnv)\("([A-Z][A-Z0-9_]*)"`)

// toolContract is set by the agent for each tool subprocess rather than read
// from configuration. Naming one of these in .env.example would invite an
// operator to set a value the agent overwrites on every call.
var toolContract = map[string]bool{
	"AGENT_URL": true, "AGENT_SESSION": true, "AGENT_JOB": true, "AGENT_TOKEN": true,
	"AGENT_DB": true, "AGENT_DB_PREFIX": true, "TMPDIR": true,
	// Granted per session rather than configured for the agent, and the value
	// still comes from the environment — so these are documented, but as
	// credentials to grant rather than as settings. See settingsAlsoGranted.
}

// settingsAlsoGranted are read from the agent's environment but reach a tool
// only in a session granted them. They belong in the file: an operator has to
// set them somewhere before any session can be granted them.
var settingsAlsoGranted = map[string]bool{"GH_TOKEN": true, "AGENT_REPO": true}

func TestEverySettingIsInTheExampleEnvironment(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", ".env.example"))
	if err != nil {
		t.Fatalf("the example environment is missing: %v", err)
	}
	documented := string(example)

	found := map[string]bool{}
	for _, root := range []string{"..", "../../cmd", "../../tools"} {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range envRead.FindAllStringSubmatch(string(b), -1) {
				found[m[1]] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) == 0 {
		t.Fatal("no environment variables found in the source; the scan is broken")
	}

	for name := range found {
		if toolContract[name] {
			continue
		}
		if !strings.Contains(documented, name) {
			t.Errorf("%s is read by the agent and is not in .env.example.\n"+
				"A setting an operator cannot find is a setting that does not exist.", name)
		}
	}
	for name := range settingsAlsoGranted {
		if !strings.Contains(documented, name) {
			t.Errorf("%s is not in .env.example, and a session cannot be granted "+
				"a variable the agent was never given", name)
		}
	}
}

// The other direction. A variable written in the file and read by nothing is
// worse than an undocumented one: it reads as a setting, and changing it does
// nothing at all.
func TestTheExampleEnvironmentNamesNothingThatIsNotRead(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}

	read := map[string]bool{}
	for _, root := range []string{"..", "../../cmd", "../../tools"} {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range envRead.FindAllStringSubmatch(string(b), -1) {
				read[m[1]] = true
			}
			return nil
		})
	}

	// An assignment in the file, commented out or not: NAME=value at line start.
	assigned := regexp.MustCompile(`(?m)^#?\s*([A-Z][A-Z0-9_]*)=`)
	for _, m := range assigned.FindAllStringSubmatch(string(example), -1) {
		name := m[1]
		if read[name] || settingsAlsoGranted[name] {
			continue
		}
		t.Errorf(".env.example offers %s, which nothing reads. A setting that does "+
			"nothing is worse than one that is missing.", name)
	}
}
