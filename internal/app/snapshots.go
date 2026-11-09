package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Before every turn, a session's working directory is snapshotted: a
// read-only copy, named after the message that starts the turn, sharing every
// block with the directory until one of them changes (ADR-082). A revert puts
// one back; a fork from an earlier point starts from one. Nothing else reads
// them.
//
// They live under one directory per session, outside every working directory,
// so no tool can reach them:
//
//	<snapshots>/<session>/<seq>     a read-only subvolume
//	<snapshots>/<session>/<seq>     or a link to another, when nothing had changed
//	<snapshots>/<session>/last      the newest one's seq and the generation after it
//
// A snapshot is named by the sequence number of the entry it stands before: the
// user message whose turn it began, or the revert event that replaced the files.

// keptSnapshots is how many a session keeps. Older ones are deleted, and a
// revert or a fork before them gets the conversation without its files.
const keptSnapshots = 20

func (a *App) snapshotsRoot() string {
	if a.cfg.Snapshots != "" {
		return a.cfg.Snapshots
	}
	// An agent built without one, as the tests build theirs, keeps them beside
	// its data. The dot keeps the name from ever being a session's.
	return filepath.Join(a.cfg.DataDir, ".snapshots")
}

func (a *App) snapshotsOf(id string) string {
	return filepath.Join(a.snapshotsRoot(), id)
}

// snapshotPath is where the snapshot before seq is, following a shared one to
// the snapshot it shares. ok is false when none is kept.
func (a *App) snapshotPath(id string, seq int) (string, bool) {
	if !validSessionID(id) {
		return "", false
	}
	p := filepath.Join(a.snapshotsOf(id), strconv.Itoa(seq))
	if to, err := os.Readlink(p); err == nil {
		p = filepath.Join(a.snapshotsOf(id), to)
	}
	if !isSubvolume(p) {
		return "", false
	}
	return p, true
}

// snapshotSeqs are the entries a session keeps a snapshot before, oldest
// first.
func (a *App) snapshotSeqs(id string) []int {
	if !validSessionID(id) {
		return nil
	}
	ents, err := os.ReadDir(a.snapshotsOf(id))
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		seq, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if _, ok := a.snapshotPath(id, seq); ok {
			out = append(out, seq)
		}
	}
	sort.Ints(out)
	return out
}

// snapshotBeforeTurn records the session's files as they are before the turn
// that the entry at seq starts. When nothing has changed since the newest
// snapshot, it shares that one rather than taking another, so a job that wakes
// every half hour and writes nothing does not push older files out.
func (a *App) snapshotBeforeTurn(id string, seq int) error {
	if !validSessionID(id) {
		return nil
	}
	ws := a.ensureWorkspace(id)
	dir := a.snapshotsOf(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := filepath.Join(dir, strconv.Itoa(seq))
	if _, err := os.Lstat(name); err == nil {
		return nil // taken already: a turn retried, say
	}
	gen, err := subvolumeGeneration(ws)
	if err != nil {
		return err
	}
	if lastSeq, lastGen, ok := a.lastSnapshot(id); ok && lastGen == gen {
		if p, kept := a.snapshotPath(id, lastSeq); kept {
			return os.Symlink(filepath.Base(p), name)
		}
	}
	return a.takeSnapshot(id, ws, seq)
}

// takeSnapshot snapshots ws under seq, records it as the newest, and lets the
// oldest go.
func (a *App) takeSnapshot(id, ws string, seq int) error {
	name := filepath.Join(a.snapshotsOf(id), strconv.Itoa(seq))
	if err := snapshotSubvolume(ws, name, true); err != nil {
		return err
	}
	// Read after the snapshot, which changes the generation of what it copied:
	// the next turn compares against the directory as the snapshot left it.
	if gen, err := subvolumeGeneration(ws); err == nil {
		a.setLastSnapshot(id, seq, gen)
	}
	a.pruneSnapshots(id)
	return nil
}

func (a *App) lastSnapshot(id string) (seq int, gen uint64, ok bool) {
	b, err := os.ReadFile(filepath.Join(a.snapshotsOf(id), "last"))
	if err != nil {
		return 0, 0, false
	}
	s, g, found := strings.Cut(strings.TrimSpace(string(b)), " ")
	if !found {
		return 0, 0, false
	}
	seq, err1 := strconv.Atoi(s)
	gen, err2 := strconv.ParseUint(g, 10, 64)
	return seq, gen, err1 == nil && err2 == nil
}

func (a *App) setLastSnapshot(id string, seq int, gen uint64) {
	_ = os.WriteFile(filepath.Join(a.snapshotsOf(id), "last"), []byte(fmt.Sprintf("%d %d\n", seq, gen)), 0o644)
}

// forgetLastSnapshot is for when the working directory was replaced, by a
// revert or a fork: the newest snapshot no longer says what it holds.
func (a *App) forgetLastSnapshot(id string) {
	_ = os.Remove(filepath.Join(a.snapshotsOf(id), "last"))
}

// pruneSnapshots deletes all but the newest keptSnapshots, and the links that
// shared the ones deleted.
func (a *App) pruneSnapshots(id string) {
	dir := a.snapshotsOf(id)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var real []int
	for _, e := range ents {
		if seq, err := strconv.Atoi(e.Name()); err == nil && e.Type()&fs.ModeSymlink == 0 {
			real = append(real, seq)
		}
	}
	sort.Ints(real)
	for len(real) > keptSnapshots {
		_ = deleteSubvolume(filepath.Join(dir, strconv.Itoa(real[0])))
		real = real[1:]
	}
	for _, e := range ents {
		if e.Type()&fs.ModeSymlink == 0 {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name())); os.IsNotExist(err) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// deleteSnapshots removes every snapshot a session has, for a session that is
// going away.
func (a *App) deleteSnapshots(id string) error {
	if !validSessionID(id) {
		return nil
	}
	dir := a.snapshotsOf(id)
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range ents {
		errs = append(errs, deleteSubvolume(filepath.Join(dir, e.Name())))
	}
	errs = append(errs, os.RemoveAll(dir))
	return errors.Join(errs...)
}

// removeAllSubvolumes is os.RemoveAll for a directory that may hold
// snapshots: a read-only one cannot be emptied, so each subvolume under dir is
// deleted as one before the rest goes.
func removeAllSubvolumes(dir string) error {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == dir || !isSubvolume(p) {
			return nil
		}
		if deleteSubvolume(p) == nil {
			return fs.SkipDir
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// replaceWorkspace puts a writable snapshot of from in place of the session's
// working directory, and returns what was there as a read-only snapshot at
// keep, so that nothing it held is lost. The swap is one rename: there is no
// moment at which the directory is missing, for an upload to land in an empty
// one created in its place.
func (a *App) replaceWorkspace(id, from, keep string) error {
	ws := a.ensureWorkspace(id)
	next := ws + ".next"
	_ = deleteSubvolume(next)
	if err := snapshotSubvolume(from, next, false); err != nil {
		return err
	}
	if keep != "" {
		if err := snapshotSubvolume(ws, keep, true); err != nil {
			_ = deleteSubvolume(next)
			return err
		}
	}
	if err := exchange(next, ws); err != nil {
		_ = deleteSubvolume(next)
		if keep != "" {
			_ = deleteSubvolume(keep)
		}
		return err
	}
	a.forgetLastSnapshot(id)
	return deleteSubvolume(next)
}

// exchange swaps two paths in one step (renameat2 with RENAME_EXCHANGE).
func exchange(a, b string) error {
	if err := unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE); err != nil {
		return &os.LinkError{Op: "exchange", Old: a, New: b, Err: err}
	}
	return nil
}
