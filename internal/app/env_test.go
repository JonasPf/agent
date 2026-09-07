package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	body := "" +
		"# a comment\n" +
		"\n" +
		"OPENROUTER_API_KEY=sk-or-plain\n" +
		"export AGENT_MODEL=anthropic/claude-opus-4.1\n" +
		"QUOTED=\"has spaces\"\n" +
		"SINGLE='single'\n" +
		"  SPACED  =  padded  \n" +
		"NOT_AN_ASSIGNMENT\n" +
		"ALREADY_SET=from-file\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALREADY_SET", "from-environment")
	for _, k := range []string{"OPENROUTER_API_KEY", "AGENT_MODEL", "QUOTED", "SINGLE", "SPACED"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	loadEnvFile(path)

	want := map[string]string{
		"OPENROUTER_API_KEY": "sk-or-plain",
		"AGENT_MODEL":        "anthropic/claude-opus-4.1",
		"QUOTED":             "has spaces",
		"SINGLE":             "single",
		"SPACED":             "padded",
		// An existing environment variable wins, so a one-off override still works.
		"ALREADY_SET": "from-environment",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if _, set := os.LookupEnv("NOT_AN_ASSIGNMENT"); set {
		t.Error("a line without = was treated as an assignment")
	}
}

func TestLoadEnvFileMissingIsFine(t *testing.T) {
	loadEnvFile(filepath.Join(t.TempDir(), "absent"))
}
