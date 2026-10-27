package app

import (
	"errors"
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

// SessionFile is one entry in a session's working directory: a file, or a
// directory, which carries no size of its own.
type SessionFile struct {
	Path       string    `json:"path"`
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modified_at"`
	Dir        bool      `json:"dir,omitempty"`
}

// sessionFiles lists a session's working directory, newest first. Directories
// are listed too, empty ones included, so the tree can be drawn as it is.
func (a *App) sessionFiles(id string) ([]SessionFile, error) {
	root := a.sessionWorkspace(id)
	out := []SessionFile{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
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
		f := SessionFile{Path: filepath.ToSlash(rel), ModifiedAt: info.ModTime(), Dir: d.IsDir()}
		if !f.Dir {
			f.Bytes = info.Size()
		}
		out = append(out, f)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out, nil
}

// maxFilesAsked is the most paths one request may ask about.
const maxFilesAsked = 200

// sessionFilesNamed answers for particular paths instead of the whole tree:
// those that are files in the session's directory, in the order asked. One
// that names nothing, a directory, or a place a link leads out to is left out
// rather than refused, because what is asked is a guess at what a conversation
// meant by a word.
func (a *App) sessionFilesNamed(id string, paths []string) ([]SessionFile, error) {
	root, err := a.workspaceRoot(id)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	out := []SessionFile{}
	seen := map[string]bool{}
	for _, p := range paths {
		rel, err := workspacePath(id, p)
		if err != nil || seen[rel] {
			continue
		}
		seen[rel] = true
		info, err := root.Stat(rel)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, SessionFile{Path: rel, Bytes: info.Size(), ModifiedAt: info.ModTime()})
	}
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
// metadata, and everything in its working directory. The transcript is two
// files and always measured; the directory is measured fresh when one session
// is asked about, and read from what is kept when the whole list is.
func (a *App) sessionDiskBytes(id string, fresh bool) int64 {
	return dirBytes(a.store.sessionDir(id)) + a.workspaceBytes(id, fresh)
}

// workspacePath cleans a path a request names into one relative to a session's
// working directory, and refuses one that is empty or climbs out of it.
func workspacePath(id, rel string) (string, error) {
	if !validSessionID(id) {
		return "", fmt.Errorf("no such session")
	}
	clean := path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("a file name is required")
	}
	return clean, nil
}

// workspaceRoot opens a session's working directory as a root: whatever is
// opened, created, or removed through it stays inside, a link on the way
// included. A tool may write a link in its directory to anywhere, because the
// sandbox confines what the tool opens and not where its links point; these
// files are served by the agent, which no sandbox confines, so the confinement
// has to be here.
func (a *App) workspaceRoot(id string) (*os.Root, error) {
	if !validSessionID(id) {
		return nil, fmt.Errorf("no such session")
	}
	return os.OpenRoot(a.ensureWorkspace(id))
}

// writeWorkspaceFile stores one file in a session's working directory, creating
// any directories its name implies.
func (a *App) writeWorkspaceFile(id, rel string, r io.Reader) (string, int64, error) {
	clean, err := workspacePath(id, rel)
	if err != nil {
		return "", 0, err
	}
	root, err := a.workspaceRoot(id)
	if err != nil {
		return "", 0, err
	}
	defer root.Close()
	if err := root.MkdirAll(path.Dir(clean), 0o755); err != nil {
		return "", 0, err
	}
	f, err := root.Create(clean)
	if err != nil {
		return "", 0, err
	}
	return a.fillWorkspaceFile(id, root, clean, f, r)
}

// writeNewWorkspaceFile stores an upload under a name nothing in the directory
// holds yet: the name it came with, then name-2, name-3, and so on. A phone
// names every photo from its camera image.jpg, and an upload that replaced
// what was there would lose the earlier photo, or a file a tool had written.
// Each name is created exclusively, so two uploads of one name at once each get
// a file of their own, and a link already holding the name is never written
// through.
func (a *App) writeNewWorkspaceFile(id, name string, r io.Reader) (string, int64, error) {
	clean, err := workspacePath(id, name)
	if err != nil {
		return "", 0, err
	}
	root, err := a.workspaceRoot(id)
	if err != nil {
		return "", 0, err
	}
	defer root.Close()
	if err := root.MkdirAll(path.Dir(clean), 0o755); err != nil {
		return "", 0, err
	}
	ext := path.Ext(clean)
	stem := strings.TrimSuffix(clean, ext)
	if path.Base(stem) == path.Dir(stem) || strings.HasSuffix(stem, "/") || path.Base(clean) == ext {
		stem, ext = clean, ""
	}
	for n := 1; n <= 1000; n++ {
		candidate := clean
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		f, err := root.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", 0, err
		}
		return a.fillWorkspaceFile(id, root, candidate, f, r)
	}
	return "", 0, fmt.Errorf("a thousand files are already named like %s", name)
}

// fillWorkspaceFile copies an upload into a file just created for it, and
// refuses it, removing the file, once it passes the limit.
func (a *App) fillWorkspaceFile(id string, root *os.Root, rel string, f *os.File, r io.Reader) (string, int64, error) {
	defer a.workspaceChanged(id)
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(r, maxUpload+1))
	if err != nil {
		// A transfer that broke off leaves no half a file behind to be read as
		// the whole of one.
		f.Close()
		_ = root.Remove(rel)
		return "", 0, err
	}
	if n > maxUpload {
		f.Close()
		_ = root.Remove(rel)
		return "", 0, fmt.Errorf("files larger than 250 MB are refused")
	}
	return rel, n, nil
}

// copyTree copies a directory recursively, so that what a tool installed in it
// still runs in the copy: a file keeps its mode, and a link is copied as a link
// rather than followed. A missing source is not an error: a session that never
// wrote a file has nothing to carry.
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
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			return copyLink(p, target, src, dst)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return copyFile(p, target, info.Mode().Perm())
		}
		return nil
	})
}

// copyLink copies the link at p to target. One that names a place inside src
// names the same place inside dst: left as it was, the copy would reach back
// into the directory it came from, which its tools cannot read and which may
// since have been deleted.
func copyLink(p, target, src, dst string) error {
	to, err := os.Readlink(p)
	if err != nil {
		return err
	}
	if filepath.IsAbs(to) {
		if rel, err := filepath.Rel(src, to); err == nil && filepath.IsLocal(rel) {
			to = filepath.Join(dst, rel)
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Symlink(to, target)
}

// copyFile copies a regular file with the permissions it had, set after the
// write so the umask does not narrow them.
func copyFile(p, target string, perm fs.FileMode) error {
	in, err := os.Open(p)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, perm)
}

// DeleteSession removes a conversation and everything it owns: its transcript,
// its metadata, its jobs, and its working directory.
func (a *App) DeleteSession(id string) error {
	// Its turns stop first, so a model call already under way is not paid for
	// to produce an answer nobody can read.
	a.stopAll(id)
	if err := a.store.DeleteSession(id); err != nil {
		return err
	}
	a.forgetSession(id)
	if !validSessionID(id) {
		return nil
	}
	return os.RemoveAll(a.sessionWorkspace(id))
}
