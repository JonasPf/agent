package app

import (
	"path/filepath"
	"testing"
)

// There were seven path settings, and nothing ever set them except the image —
// where they resolved to two roots and always had. State is what outlives the
// container and is mounted; home is what the image ships and never changes.
// Seven lines had to be kept in step with one layout decision, which is six
// chances to get it wrong for no choice anybody wanted.
func TestTheLayoutIsTwoRootsAndNotSevenSettings(t *testing.T) {
	t.Setenv("AGENT_STATE", "/app/state")
	t.Setenv("AGENT_HOME", "/app")
	// Without this the real .env is read and the test depends on the machine.
	t.Setenv("AGENT_STATE_ENV_MISSING", "")

	cfg := LoadConfig()
	for _, c := range []struct {
		name, got, want string
	}{
		{"data", cfg.DataDir, "/app/state/data"},
		{"workspace", cfg.Workspace, "/app/state/workspace"},
		{"settings file", cfg.EnvFile, "/app/state/.env"},
		{"tools", cfg.ToolsDir, "/app/tools"},
		{"skills", cfg.SkillsDir, "/app/skills"},
		{"web", cfg.WebDir, "/app/web"},
		{"changelog", cfg.ChangelogPath, "/app/CHANGELOG.md"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// Unset, both roots are the working directory, which is what running from a
// checkout has always meant: data/ and workspace/ beside tools/ and skills/.
func TestUnsetRootsMeanTheWorkingDirectory(t *testing.T) {
	t.Setenv("AGENT_STATE", "")
	t.Setenv("AGENT_HOME", "")

	cfg := LoadConfig()
	for _, c := range []struct {
		name, got, want string
	}{
		{"data", cfg.DataDir, filepath.Join(".", "data")},
		{"workspace", cfg.Workspace, filepath.Join(".", "workspace")},
		{"settings file", cfg.EnvFile, filepath.Join(".", ".env")},
		{"tools", cfg.ToolsDir, filepath.Join(".", "tools")},
		{"changelog", cfg.ChangelogPath, filepath.Join(".", "CHANGELOG.md")},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// The numbers that decide how much conversation is kept were settings nothing
// ever set, and two of them were defaults for a default: compact_at_tokens and
// keep_verbatim_tokens are already per-session and editable on the screen the
// session is read from. A constant says the same thing without appearing on a
// list of things an operator has to consider.
func TestTheConversationSizesAreConstantsAndNotSettings(t *testing.T) {
	t.Setenv("AGENT_COMPACT_TOKENS", "1")
	t.Setenv("AGENT_KEEP_TOKENS", "2")
	t.Setenv("AGENT_SUMMARY_EVERY", "3")
	t.Setenv("AGENT_MEMORY_CAPACITY", "4")

	cfg := LoadConfig()
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"compact at", cfg.CompactAtTokens, defaultCompactAtTokens},
		{"keep verbatim", cfg.KeepVerbatimTokens, defaultKeepVerbatimTokens},
		{"summary every", cfg.SummaryEvery, defaultSummaryEvery},
		{"memory capacity", cfg.MemoryCapacity, defaultMemoryCapacity},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want the constant %d — the environment must not decide this",
				c.name, c.got, c.want)
		}
	}
}
