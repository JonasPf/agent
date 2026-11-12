package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// The bundles and skills the image ships are named in plugins.lock, checked in
// beside the code: for each, its version, where its archive is, and the sha256
// it must have. So what the agent ships with changes only in a commit, like
// the rest of it (ADR-088).

// LockedPlugin is one entry of plugins.lock.
type LockedPlugin struct {
	// Kind is "skill" for a skill and empty for a bundle.
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Files is the archive for each architecture, by Go's name for it, or
	// under "any" for one that is the same everywhere, as a skill's is.
	Files map[string]struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

// lockMark is the file in an unpacked archive saying which one it came from,
// so a second fetch of the same lock downloads nothing, and so a folder this
// fetched can be told from one that was put there another way.
const lockMark = ".sha256"

// FetchPlugins unpacks each bundle the lock names into plugins/<name> and
// each skill into skills/<name>, for arch. An archive whose sha256 is not the
// one the lock gives is not unpacked, and the fetch fails. A folder an earlier
// fetch made and this lock does not name is removed; one that was never
// fetched is left alone.
func FetchPlugins(lockPath, plugins, skills, arch string, client *http.Client, log io.Writer) error {
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		return err
	}
	var lock []LockedPlugin
	if err := json.Unmarshal(raw, &lock); err != nil {
		return fmt.Errorf("%s: %w", lockPath, err)
	}
	named := map[string]bool{}
	for _, p := range lock {
		out := plugins
		switch p.Kind {
		case "":
		case "skill":
			out = skills
		default:
			return fmt.Errorf("%s: %s is a %q, which is neither a bundle nor a skill", lockPath, p.Name, p.Kind)
		}
		if err := validAuthoredName(p.Name); err != nil {
			return fmt.Errorf("%s: %w", lockPath, err)
		}
		f, ok := p.Files[arch]
		if !ok {
			f, ok = p.Files["any"]
		}
		if !ok {
			return fmt.Errorf("%s: %s %s has no archive for %s", lockPath, p.Name, p.Version, arch)
		}
		target := filepath.Join(out, p.Name)
		named[target] = true
		if have, _ := os.ReadFile(filepath.Join(target, lockMark)); strings.TrimSpace(string(have)) == f.SHA256 {
			continue
		}
		if err := os.MkdirAll(out, 0o755); err != nil {
			return err
		}
		a := &App{cfg: Config{DataDir: out}, fetch: client}
		archive, sum, err := a.archiveFrom(f.URL, nil, maxBundle)
		if err != nil {
			return fmt.Errorf("%s %s: %w", p.Name, p.Version, err)
		}
		defer os.Remove(archive)
		if !strings.EqualFold(sum, f.SHA256) {
			return fmt.Errorf("%s %s: %s has sha256 %s, and the lock says %s", p.Name, p.Version, f.URL, sum, f.SHA256)
		}
		tmp, err := os.MkdirTemp(out, ".new-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err = os.Chmod(tmp, 0o755); err == nil {
			err = unpack(archive, tmp)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(tmp, lockMark), []byte(f.SHA256+"\n"), 0o644)
		}
		if err == nil {
			err = os.RemoveAll(target)
		}
		if err == nil {
			err = os.Rename(tmp, target)
		}
		if err != nil {
			return fmt.Errorf("%s %s: %w", p.Name, p.Version, err)
		}
		fmt.Fprintf(log, "fetched %s %s\n", p.Name, p.Version)
	}
	for _, out := range []string{plugins, skills} {
		entries, _ := os.ReadDir(out)
		for _, e := range entries {
			dir := filepath.Join(out, e.Name())
			if _, err := os.Stat(filepath.Join(dir, lockMark)); err != nil || named[dir] {
				continue
			}
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		}
	}
	return nil
}
