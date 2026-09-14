package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The operator has to be able to tell which build is answering: the deployment
// names images by commit, so the version the interface shows and the tag a
// rollback names must be the same string. The changelog is a file that ships
// with the app — the container has no repository to derive one from.
func TestVersionReportsTheBuildAndTheChangelog(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# Changelog\n\n## 2026-09-11\n\n- Compact in place.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.cfg.ChangelogPath = path

	old, oldAt := Version, BuiltAt
	Version, BuiltAt = "7da7e74", "2026-09-11T10:00:00Z"
	defer func() { Version, BuiltAt = old, oldAt }()

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/version", nil))
	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}
	var got struct {
		Version   string `json:"version"`
		BuiltAt   string `json:"built_at"`
		Changelog string `json:"changelog"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "7da7e74" {
		t.Errorf("version = %q, want the stamped commit", got.Version)
	}
	if got.BuiltAt != "2026-09-11T10:00:00Z" {
		t.Errorf("built_at = %q, want the stamped build time", got.BuiltAt)
	}
	if !strings.Contains(got.Changelog, "Compact in place.") {
		t.Errorf("changelog = %q, want the file's contents", got.Changelog)
	}
}

// A binary built without a stamp must say so. A number invented at runtime would
// be worse than none: it would name a build nobody can roll back to.
func TestAnUnstampedBuildSaysDev(t *testing.T) {
	if Version != "dev" {
		t.Errorf("Version = %q, want %q by default", Version, "dev")
	}
	if BuiltAt != "" {
		t.Errorf("BuiltAt = %q, want empty by default", BuiltAt)
	}
}

// A missing changelog leaves the version readable. The file is shipped, not
// generated, so its absence is a packaging mistake and must not take the screen
// down with it.
func TestAMissingChangelogStillAnswers(t *testing.T) {
	a := newTestApp(t)
	a.cfg.ChangelogPath = filepath.Join(t.TempDir(), "absent.md")

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/version", nil))
	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["version"] == "" || got["version"] == nil {
		t.Error("a missing changelog must not take the version with it")
	}
	if got["changelog"] != "" {
		t.Errorf("changelog = %v, want empty", got["changelog"])
	}
}

// The changelog that actually ships is the one in the repository root, and the
// app is configured to find it there by default.
func TestTheShippedChangelogIsTheDefault(t *testing.T) {
	if def := (Config{}); def.ChangelogPath != "" {
		t.Fatalf("zero Config carries a path: %q", def.ChangelogPath)
	}
	t.Setenv("AGENT_ENV", filepath.Join(t.TempDir(), "none.env"))
	cfg := LoadConfig()
	if cfg.ChangelogPath != "CHANGELOG.md" {
		t.Errorf("ChangelogPath = %q, want CHANGELOG.md", cfg.ChangelogPath)
	}
	if _, err := os.Stat(filepath.Join("..", "..", cfg.ChangelogPath)); err != nil {
		t.Errorf("the shipped changelog is missing: %v", err)
	}
}

// The changelog opens with a note to whoever writes it: how the file is
// organised, and that a change to behaviour belongs in it. That is a rule for
// working in this repository, and the version screen is not where it is read —
// it is read on a phone, once, after an upgrade, by someone who wants to know
// what changed. So the screen is served the entries and nothing above them.
func TestTheVersionScreenIsServedTheEntriesAndNotTheNoteToWriters(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	body := "# Changelog\n\n" +
		"What changed, newest first. A change to behaviour belongs here in the\n" +
		"same commit that makes it.\n\n" +
		"## 2026-09-14\n\n- One volume, and no compose file.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a.cfg.ChangelogPath = path

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/version", nil))
	var got struct {
		Changelog string `json:"changelog"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Changelog, "## 2026-09-14") {
		t.Errorf("changelog starts %q, want the first dated section", truncate(got.Changelog, 60))
	}
	for _, unwanted := range []string{"# Changelog", "newest first", "same commit"} {
		if strings.Contains(got.Changelog, unwanted) {
			t.Errorf("the screen was served %q, which is a note to whoever writes the file", unwanted)
		}
	}
	if !strings.Contains(got.Changelog, "One volume, and no compose file.") {
		t.Error("the entries themselves did not survive")
	}
}

// A file with no dated section has no entries, and says so by being empty —
// which is the state the interface already reports as a changelog it cannot
// show. Serving the preamble instead would put the note to writers on the
// screen precisely when there is nothing to distract from it.
func TestAChangelogWithNoEntriesServesNothing(t *testing.T) {
	a := newTestApp(t)
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# Changelog\n\nNothing has happened yet.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.cfg.ChangelogPath = path

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/version", nil))
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["changelog"] != "" {
		t.Errorf("changelog = %v, want empty", got["changelog"])
	}
	if got["version"] == "" || got["version"] == nil {
		t.Error("an entryless changelog must not take the version with it")
	}
}
