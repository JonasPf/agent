package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

// A session's working directory is a btrfs subvolume, and the agent snapshots
// it before every turn (ADR-082). A snapshot of a subvolume is one operation
// whatever the directory holds: the copy shares every block with the original
// until one side writes. That is what makes a snapshot per turn affordable, a
// revert able to put files back, and a fork free.
//
// These are the five ioctls it takes, called directly, as Landlock is. None
// of them needs privilege on a filesystem mounted user_subvol_rm_allowed: a
// user may create subvolumes, snapshot the ones it owns, and delete the ones
// it may write.

const (
	btrfsSuperMagic = 0x9123683E
	// Every subvolume's root directory has this inode number, and no other
	// directory does.
	btrfsFirstFreeObjectID = 256

	// _IOW/_IOR(0x94, nr, the argument): the size is part of the request.
	btrfsIocSubvolCreate   = 0x5000940E // _IOW(0x94, 14, btrfs_ioctl_vol_args)
	btrfsIocSnapDestroy    = 0x5000940F // _IOW(0x94, 15, btrfs_ioctl_vol_args)
	btrfsIocSnapCreateV2   = 0x50009417 // _IOW(0x94, 23, btrfs_ioctl_vol_args_v2)
	btrfsIocSubvolGetflags = 0x80089419 // _IOR(0x94, 25, __u64)
	btrfsIocSubvolSetflags = 0x4008941A // _IOW(0x94, 26, __u64)
	btrfsIocGetSubvolInfo  = 0x81F8943C // _IOR(0x94, 60, btrfs_ioctl_get_subvol_info_args)

	btrfsSubvolRdonly = 1 << 1
)

// btrfs_ioctl_vol_args.
type btrfsVolArgs struct {
	fd   int64
	name [4088]byte
}

// btrfs_ioctl_vol_args_v2, with its unions laid out as their largest member.
type btrfsVolArgsV2 struct {
	fd      int64
	transid uint64
	flags   uint64
	unused  [4]uint64
	name    [4040]byte
}

// btrfs_ioctl_get_subvol_info_args. A btrfs_ioctl_timespec is a u64 and a u32,
// padded to sixteen bytes.
type btrfsSubvolInfo struct {
	treeid       uint64
	name         [256]byte
	parentID     uint64
	dirid        uint64
	generation   uint64
	flags        uint64
	uuid         [16]byte
	parentUUID   [16]byte
	receivedUUID [16]byte
	ctransid     uint64
	otransid     uint64
	stransid     uint64
	rtransid     uint64
	times        [4][2]uint64
	reserved     [8]uint64
}

func btrfsIoctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg))
	runtime.KeepAlive(f)
	if errno != 0 {
		return errno
	}
	return nil
}

// onBtrfs reports whether path is on a btrfs filesystem.
func onBtrfs(path string) (bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return false, err
	}
	return uint32(st.Type) == btrfsSuperMagic, nil
}

// isSubvolume reports whether path is the root of a btrfs subvolume.
func isSubvolume(path string) bool {
	var st syscall.Stat_t
	if syscall.Lstat(path, &st) != nil {
		return false
	}
	if st.Ino != btrfsFirstFreeObjectID || st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return false
	}
	ok, _ := onBtrfs(path)
	return ok
}

// subvolumeCall makes one of the ioctls that are asked of the directory a
// subvolume is in, naming the subvolume. name is the field the kernel reads
// it from; it must keep a terminating zero.
func subvolumeCall(op, path string, name []byte, req uintptr, arg unsafe.Pointer) error {
	base := filepath.Base(path)
	if len(base) >= len(name) {
		return &os.PathError{Op: op, Path: path, Err: syscall.ENAMETOOLONG}
	}
	copy(name, base)
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := btrfsIoctl(parent, req, arg); err != nil {
		return &os.PathError{Op: op, Path: path, Err: err}
	}
	return nil
}

// createSubvolume makes path a new, empty subvolume. Its parent must exist.
func createSubvolume(path string) error {
	var args btrfsVolArgs
	return subvolumeCall("create subvolume", path, args.name[:], btrfsIocSubvolCreate, unsafe.Pointer(&args))
}

// snapshotSubvolume makes dst a snapshot of the subvolume src: read-only for
// a turn's record, writable for a fork or a revert to carry on in.
func snapshotSubvolume(src, dst string, readOnly bool) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	var args btrfsVolArgsV2
	args.fd = int64(s.Fd())
	if readOnly {
		args.flags = btrfsSubvolRdonly
	}
	err = subvolumeCall("snapshot "+src+" as", dst, args.name[:], btrfsIocSnapCreateV2, unsafe.Pointer(&args))
	runtime.KeepAlive(s)
	return err
}

// setSubvolumeReadOnly turns a subvolume's read-only flag on or off.
func setSubvolumeReadOnly(path string, ro bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var flags uint64
	if err := btrfsIoctl(f, btrfsIocSubvolGetflags, unsafe.Pointer(&flags)); err != nil {
		return &os.PathError{Op: "read subvolume flags", Path: path, Err: err}
	}
	if ro == (flags&btrfsSubvolRdonly != 0) {
		return nil
	}
	flags ^= btrfsSubvolRdonly
	if err := btrfsIoctl(f, btrfsIocSubvolSetflags, unsafe.Pointer(&flags)); err != nil {
		return &os.PathError{Op: "set subvolume flags", Path: path, Err: err}
	}
	return nil
}

// deleteSubvolume removes the subvolume at path and everything in it. What is
// already gone is not an error, and a plain directory is removed the plain
// way, which is what a session's directory was before ADR-082.
func deleteSubvolume(path string) error {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	if !isSubvolume(path) {
		return os.RemoveAll(path)
	}
	// A user who is not root may delete only a subvolume it may write, and
	// nobody may write a read-only one.
	if err := setSubvolumeReadOnly(path, false); err != nil {
		return err
	}
	var args btrfsVolArgs
	err := subvolumeCall("delete subvolume", path, args.name[:], btrfsIocSnapDestroy, unsafe.Pointer(&args))
	if errors.Is(err, syscall.ENOTEMPTY) {
		// A subvolume a tool made inside its own directory: the ioctl will
		// not take two at once, but emptying them one directory at a time
		// will, since an empty subvolume is removed like a directory.
		return os.RemoveAll(path)
	}
	return err
}

// subvolumeGeneration is the transaction that last changed the subvolume at
// path. It moves when anything in it changes, and only then, so the same
// number twice means nothing was written in between. What is still only in
// memory is written out first: until it is committed, a change has not moved
// the number.
func subvolumeGeneration(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, _, errno := syscall.Syscall(syscall.SYS_SYNCFS, f.Fd(), 0, 0); errno != 0 {
		return 0, &os.PathError{Op: "syncfs", Path: path, Err: errno}
	}
	var info btrfsSubvolInfo
	if err := btrfsIoctl(f, btrfsIocGetSubvolInfo, unsafe.Pointer(&info)); err != nil {
		return 0, &os.PathError{Op: "read subvolume info", Path: path, Err: err}
	}
	return info.generation, nil
}

// checkBtrfs is what the agent asks of the directories it keeps
// conversations' files and their snapshots in before it starts: that they are
// btrfs, and that this user can do there everything a turn, a revert, a fork,
// and a delete will ask of it. The answer comes from doing it once, which is
// the only way to know the mount lets a user who is not root delete what it
// made, and that a snapshot can be made from one into the other.
func checkBtrfs(dir, snapshots string) error {
	for _, d := range []string{dir, snapshots} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		if ok, err := onBtrfs(d); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%s is not on btrfs. The agent keeps every conversation's files in a "+
				"btrfs subvolume and snapshots it before each turn (ADR-082); mount a btrfs "+
				"filesystem there, with the options noatime,user_subvol_rm_allowed", d)
		}
	}
	probe := filepath.Join(dir, fmt.Sprintf(".btrfs-check-%d", os.Getpid()))
	snap := filepath.Join(snapshots, fmt.Sprintf(".btrfs-check-%d", os.Getpid()))
	_ = deleteSubvolume(snap)
	_ = deleteSubvolume(probe)
	err := createSubvolume(probe)
	if err == nil {
		err = snapshotSubvolume(probe, snap, true)
	}
	if err == nil {
		_, err = subvolumeGeneration(probe)
	}
	if err == nil {
		err = deleteSubvolume(snap)
	}
	if err == nil {
		err = deleteSubvolume(probe)
	}
	if err != nil {
		return fmt.Errorf("%s is btrfs, but this user cannot snapshot it into %s: %w. Are both on "+
			"one filesystem, mounted with user_subvol_rm_allowed, and owned by the user the agent "+
			"runs as? (ADR-082)", dir, snapshots, err)
	}
	return nil
}
