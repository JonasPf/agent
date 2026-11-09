package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// The kernel names an ioctl by the size of what it carries, so a struct one
// byte off is a different request, refused as unknown.
func TestBtrfsIoctlArgumentsHaveTheKernelsSize(t *testing.T) {
	for name, got := range map[string]uintptr{
		"btrfs_ioctl_vol_args":             unsafe.Sizeof(btrfsVolArgs{}),
		"btrfs_ioctl_vol_args_v2":          unsafe.Sizeof(btrfsVolArgsV2{}),
		"btrfs_ioctl_get_subvol_info_args": unsafe.Sizeof(btrfsSubvolInfo{}),
	} {
		want := map[string]uintptr{
			"btrfs_ioctl_vol_args":             4096,
			"btrfs_ioctl_vol_args_v2":          4096,
			"btrfs_ioctl_get_subvol_info_args": 504,
		}[name]
		if got != want {
			t.Errorf("%s is %d bytes, the kernel's is %d", name, got, want)
		}
	}
}

func TestTheTemporaryDirectoryIsBtrfs(t *testing.T) {
	ok, err := onBtrfs(t.TempDir())
	if err != nil || !ok {
		t.Fatalf("onBtrfs(TempDir) = %v, %v; TestMain should have refused to run", ok, err)
	}
	ok, err = onBtrfs("/proc")
	if err != nil || ok {
		t.Fatalf("onBtrfs(/proc) = %v, %v; want false", ok, err)
	}
}

// A read-only snapshot keeps the directory as it was; the directory goes on
// changing, and the snapshot cannot be written.
func TestASnapshotKeepsTheDirectoryAsItWas(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := createSubvolume(ws); err != nil {
		t.Fatal(err)
	}
	if !isSubvolume(ws) {
		t.Fatal("a created subvolume is not reported as one")
	}
	if isSubvolume(dir) {
		t.Fatal("a plain directory is reported as a subvolume")
	}
	mustWrite(t, filepath.Join(ws, "notes.txt"), "before")
	mustWrite(t, filepath.Join(ws, "sub", "deep.txt"), "deep")

	snap := filepath.Join(dir, "snap")
	if err := snapshotSubvolume(ws, snap, true); err != nil {
		t.Fatal(err)
	}
	dropSubvolume(t, snap)
	mustWrite(t, filepath.Join(ws, "notes.txt"), "after")
	if err := os.Remove(filepath.Join(ws, "sub", "deep.txt")); err != nil {
		t.Fatal(err)
	}

	if got := mustRead(t, filepath.Join(snap, "notes.txt")); got != "before" {
		t.Errorf("the snapshot reads %q, want %q", got, "before")
	}
	if got := mustRead(t, filepath.Join(snap, "sub", "deep.txt")); got != "deep" {
		t.Errorf("the snapshot lost a file deleted after it: %q", got)
	}
	err := os.WriteFile(filepath.Join(snap, "notes.txt"), []byte("x"), 0o644)
	if !errors.Is(err, syscall.EROFS) {
		t.Errorf("writing into a read-only snapshot: %v, want EROFS", err)
	}
}

// A writable snapshot is a copy: what either side writes afterwards, the
// other does not see.
func TestAWritableSnapshotIsIndependent(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := createSubvolume(a); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(a, "f"), "one")
	if err := snapshotSubvolume(a, b, false); err != nil {
		t.Fatal(err)
	}
	if !isSubvolume(b) {
		t.Fatal("a snapshot is not a subvolume")
	}
	mustWrite(t, filepath.Join(b, "f"), "two")
	mustWrite(t, filepath.Join(a, "g"), "three")
	if got := mustRead(t, filepath.Join(a, "f")); got != "one" {
		t.Errorf("the origin reads %q after the copy was written", got)
	}
	if _, err := os.Stat(filepath.Join(b, "g")); !os.IsNotExist(err) {
		t.Errorf("the copy sees a file written in the origin after it: %v", err)
	}
}

func TestDeletingASubvolume(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := createSubvolume(ws); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(ws, "f"), "x")
	snap := filepath.Join(dir, "snap")
	if err := snapshotSubvolume(ws, snap, true); err != nil {
		t.Fatal(err)
	}
	dropSubvolume(t, snap)
	for _, p := range []string{snap, ws} {
		if err := deleteSubvolume(p); err != nil {
			t.Fatalf("delete %s: %v", p, err)
		}
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s is still there: %v", p, err)
		}
	}
	// Gone already is not an error, and a plain directory goes the plain way.
	if err := deleteSubvolume(ws); err != nil {
		t.Errorf("deleting what is gone: %v", err)
	}
	plain := filepath.Join(dir, "plain")
	mustWrite(t, filepath.Join(plain, "f"), "x")
	if err := deleteSubvolume(plain); err != nil {
		t.Errorf("deleting a plain directory: %v", err)
	}
	if _, err := os.Lstat(plain); !os.IsNotExist(err) {
		t.Errorf("the plain directory is still there: %v", err)
	}
}

// The generation is how a turn knows nothing changed since the last snapshot,
// so it must move for every kind of change, including one still only in
// memory, and stay put for none.
func TestTheGenerationMovesOnEveryChangeAndOnlyThen(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := createSubvolume(ws); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(ws, "f"), "x")
	mustWrite(t, filepath.Join(ws, "d", "g"), "y")
	snap := filepath.Join(dir, "snap")
	if err := snapshotSubvolume(ws, snap, true); err != nil {
		t.Fatal(err)
	}
	dropSubvolume(t, snap)
	gen := func() uint64 {
		t.Helper()
		g, err := subvolumeGeneration(ws)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	base := gen()
	// Reading changes nothing that counts.
	_ = mustRead(t, filepath.Join(ws, "f"))
	if _, err := os.ReadDir(ws); err != nil {
		t.Fatal(err)
	}
	if g := gen(); g != base {
		t.Fatalf("nothing changed, and the generation moved from %d to %d", base, g)
	}

	changes := []struct {
		what string
		do   func() error
	}{
		{"a write not yet synced", func() error {
			f, err := os.OpenFile(filepath.Join(ws, "f"), os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				return err
			}
			_, err = f.WriteString("more")
			// Not closed until after the generation is read: still in memory.
			t.Cleanup(func() { f.Close() })
			return err
		}},
		{"a new file in a subdirectory", func() error {
			return os.WriteFile(filepath.Join(ws, "d", "new"), []byte("n"), 0o644)
		}},
		{"a deletion", func() error { return os.Remove(filepath.Join(ws, "d", "g")) }},
		{"a rename", func() error { return os.Rename(filepath.Join(ws, "d", "new"), filepath.Join(ws, "moved")) }},
		{"a mode change", func() error { return os.Chmod(filepath.Join(ws, "moved"), 0o755) }},
		{"a link", func() error { return os.Symlink("moved", filepath.Join(ws, "ln")) }},
	}
	for _, c := range changes {
		before := gen()
		if err := c.do(); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if after := gen(); after == before {
			t.Errorf("after %s the generation is still %d", c.what, before)
		}
	}
}

// The agent is not root where it is deployed. A user may snapshot the
// subvolumes it owns, and, on a filesystem mounted user_subvol_rm_allowed,
// delete them — a read-only one only once it is made writable again. Under
// test the suite is root, which may do all of this anyway, so the same steps
// are run again by this binary as a user who is not.
func TestSnapshotsWorkForAUserWhoIsNotRoot(t *testing.T) {
	dir := t.TempDir()
	// The test binary sits in the go tool's private directory, and the
	// temporary directory's parent is the test's alone: the other user can
	// reach neither, so it gets its own copy in a place it can.
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(filepath.Dir(dir), "app.test")
	if err := os.WriteFile(bin, self, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-btrfs-as-user", dir)
	if os.Getuid() == 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("as uid 65534: %v\n%s", err, out)
	}
}

// btrfsAsUser is what TestSnapshotsWorkForAUserWhoIsNotRoot runs in its child.
func btrfsAsUser(dir string) error {
	ws, snap := filepath.Join(dir, "ws"), filepath.Join(dir, "snap")
	if err := createSubvolume(ws); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ws, "f"), []byte("x"), 0o644); err != nil {
		return err
	}
	if err := snapshotSubvolume(ws, snap, true); err != nil {
		return err
	}
	if _, err := subvolumeGeneration(ws); err != nil {
		return err
	}
	for _, p := range []string{snap, ws} {
		if err := deleteSubvolume(p); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(ws); !os.IsNotExist(err) {
		return fmt.Errorf("%s survived its delete: %v", ws, err)
	}
	fmt.Println("ok")
	return nil
}

// dropSnapshots deletes, when the test ends, every snapshot the agent took.
// A snapshot is read-only, so the testing package's own removal of the
// temporary directory cannot empty it; registered after that directory was
// made, this runs before that removal does.
func dropSnapshots(t *testing.T, a *App) {
	t.Helper()
	t.Cleanup(func() {
		ents, _ := os.ReadDir(a.snapshotsRoot())
		for _, e := range ents {
			if err := a.deleteSnapshots(e.Name()); err != nil {
				t.Errorf("deleting the snapshots of %s: %v", e.Name(), err)
			}
		}
	})
}

// dropSubvolume deletes a read-only snapshot a test made itself.
func dropSubvolume(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		if err := deleteSubvolume(path); err != nil {
			t.Errorf("deleting %s: %v", path, err)
		}
	})
}

func mustWrite(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
