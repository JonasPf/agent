package app

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A session owns a working directory: the place its tools run and its uploads
// land. One conversation's files are therefore never visible to another, and a
// session's files travel with it — copied when it is forked, deleted with it,
// and carried in its export.
//
// The directories live under the configured workspace, one per session id.

// sessionWorkspace is the directory a session's tools run in. An empty or
// unrecognised id falls back to the workspace root, so a tool called outside any
// session — from a panel — still has somewhere to run.
func (a *App) sessionWorkspace(id string) string {
	if !validSessionID(id) {
		return a.cfg.Workspace
	}
	return filepath.Join(a.cfg.Workspace, id)
}

// ensureWorkspace returns the session's working directory, creating it.
func (a *App) ensureWorkspace(id string) string {
	dir := a.sessionWorkspace(id)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// validSessionID reports whether an id is safe to use as a directory name. Ids
// are generated hex, so anything else arrived from outside.
func validSessionID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// SessionFile is one file in a session's working directory.
type SessionFile struct {
	Path       string    `json:"path"`
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modified_at"`
}

// sessionFiles lists a session's working directory, newest first.
func (a *App) sessionFiles(id string) ([]SessionFile, error) {
	root := a.sessionWorkspace(id)
	out := []SessionFile{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		out = append(out, SessionFile{Path: filepath.ToSlash(rel), Bytes: info.Size(),
			ModifiedAt: info.ModTime()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out, nil
}

// dirBytes is what a directory occupies, counting regular files only. A missing
// directory is zero rather than an error.
func dirBytes(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// sessionDiskBytes is what deleting a session would free: its transcript and
// metadata, and everything in its working directory.
func (a *App) sessionDiskBytes(id string) int64 {
	return dirBytes(a.store.sessionDir(id)) + dirBytes(a.sessionWorkspace(id))
}

// resolveInWorkspace joins a relative path to a session's working directory and
// refuses anything that would leave it.
func (a *App) resolveInWorkspace(id, rel string) (string, error) {
	if !validSessionID(id) {
		return "", fmt.Errorf("no such session")
	}
	clean := path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("a file name is required")
	}
	full := filepath.Join(a.sessionWorkspace(id), filepath.FromSlash(clean))
	root := a.sessionWorkspace(id) + string(os.PathSeparator)
	if !strings.HasPrefix(full, root) {
		return "", fmt.Errorf("path leaves the session's working directory")
	}
	return full, nil
}

// writeWorkspaceFile stores one file in a session's working directory, creating
// any directories its name implies.
func (a *App) writeWorkspaceFile(id, rel string, r io.Reader) (string, int64, error) {
	full, err := a.resolveInWorkspace(id, rel)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", 0, err
	}
	f, err := os.Create(full)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(r, maxUpload+1))
	if err != nil {
		return "", 0, err
	}
	if n > maxUpload {
		f.Close()
		_ = os.Remove(full)
		return "", 0, fmt.Errorf("files larger than 100 MB are refused")
	}
	rel, _ = filepath.Rel(a.sessionWorkspace(id), full)
	return filepath.ToSlash(rel), n, nil
}

// copyTree copies a directory recursively. A missing source is not an error: a
// session that never wrote a file has nothing to carry.
func copyTree(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// DeleteSession removes a conversation and everything it owns: its transcript,
// its metadata, its jobs, and its working directory.
func (a *App) DeleteSession(id string) error {
	if err := a.store.DeleteSession(id); err != nil {
		return err
	}
	if !validSessionID(id) {
		return nil
	}
	return os.RemoveAll(a.sessionWorkspace(id))
}
