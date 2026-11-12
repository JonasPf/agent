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

// The bundles the image ships are named in plugins.lock, checked in beside
// the code: for each, its version, and for each architecture where its
// archive is and the sha256 it must have. So the tools the agent ships with
// change only in a commit, like the rest of it (ADR-088).

// LockedPlugin is one entry of plugins.lock.
type LockedPlugin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Files is the archive for each architecture, by Go's name for it.
	Files map[string]struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

// lockMark is the file in an unpacked bundle saying which archive it came
// from, so a second fetch of the same lock downloads nothing.
const lockMark = ".sha256"

// FetchPlugins unpacks each bundle the lock names, for arch, into
// out/<name>. An archive whose sha256 is not the one the lock gives is not
// unpacked, and the fetch fails. A folder in out the lock does not name is
// removed: what is there afterwards is what the lock says.
func FetchPlugins(lockPath, out, arch string, client *http.Client, log io.Writer) error {
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		return err
	}
	var lock []LockedPlugin
	if err := json.Unmarshal(raw, &lock); err != nil {
		return fmt.Errorf("%s: %w", lockPath, err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	a := &App{cfg: Config{DataDir: out}, fetch: client}
	named := map[string]bool{}
	for _, p := range lock {
		if err := validAuthoredName(p.Name); err != nil {
			return fmt.Errorf("%s: %w", lockPath, err)
		}
		named[p.Name] = true
		f, ok := p.Files[arch]
		if !ok {
			return fmt.Errorf("%s: %s %s has no archive for %s", lockPath, p.Name, p.Version, arch)
		}
		target := filepath.Join(out, p.Name)
		if have, _ := os.ReadFile(filepath.Join(target, lockMark)); strings.TrimSpace(string(have)) == f.SHA256 {
			continue
		}
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
	entries, err := os.ReadDir(out)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() && !named[e.Name()] {
			if err := os.RemoveAll(filepath.Join(out, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
