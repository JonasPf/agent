//go:build linux

package app

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// Landlock: a kernel boundary an unprivileged process can ask for. It needs no
// namespace, no capability, and no cooperation from the host — which is what
// bubblewrap needed and could not get inside a container on a distribution that
// refuses unprivileged user namespaces.
//
// The syscalls are three and the structures are two, so they are written out
// here rather than taken as a dependency.
const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446

	// prctl(2). Not in Go's syscall package for Linux, so it is named here.
	prSetNoNewPrivs = 38
	// open(2). O_PATH resolves a path without opening the file behind it.
	oPath = 0x200000

	landlockCreateRulesetVersion = 1 << 0
	landlockRuleTypePathBeneath  = 1

	// Filesystem access rights, by the ABI that introduced them.
	fsExecute    = 1 << 0
	fsWriteFile  = 1 << 1
	fsReadFile   = 1 << 2
	fsReadDir    = 1 << 3
	fsRemoveDir  = 1 << 4
	fsRemoveFile = 1 << 5
	fsMakeChar   = 1 << 6
	fsMakeDir    = 1 << 7
	fsMakeReg    = 1 << 8
	fsMakeSock   = 1 << 9
	fsMakeFifo   = 1 << 10
	fsMakeBlock  = 1 << 11
	fsMakeSym    = 1 << 12
	fsRefer      = 1 << 13 // ABI 2
	fsTruncate   = 1 << 14 // ABI 3

	abi1Rights = fsExecute | fsWriteFile | fsReadFile | fsReadDir | fsRemoveDir |
		fsRemoveFile | fsMakeChar | fsMakeDir | fsMakeReg | fsMakeSock |
		fsMakeFifo | fsMakeBlock | fsMakeSym

	// Reading is executing, reading a file, and listing a directory. Nothing
	// else: a path granted this cannot be written, created in, or removed from.
	readRights = fsExecute | fsReadFile | fsReadDir
	// A file handed to a tool by name — the database and its journals.
	fileRights = fsReadFile | fsWriteFile
)

// rulesetAttr is struct landlock_ruleset_attr as of ABI 1. Later ABIs append
// fields; passing the ABI 1 size asks the kernel for a filesystem-only ruleset,
// which is the whole of what this needs.
type rulesetAttr struct{ HandledAccessFS uint64 }

// handledFor is every right the running kernel knows about. A right that is not
// handled is a right the ruleset cannot deny, so this grows with the ABI rather
// than being pinned: on a kernel that understands truncation, an unhandled
// truncate would be an unrestricted one.
func handledFor(abi int) uint64 {
	rights := uint64(abi1Rights)
	if abi >= 2 {
		rights |= fsRefer
	}
	if abi >= 3 {
		rights |= fsTruncate
	}
	return rights
}

// landlockABI asks the kernel which version of Landlock it implements. A
// negative answer means the syscall exists and the LSM is off; ENOSYS means the
// kernel predates it.
func landlockABI() (int, error) {
	v, _, errno := syscall.Syscall(sysLandlockCreateRuleset, 0, 0, landlockCreateRulesetVersion)
	if errno != 0 {
		return 0, errno
	}
	return int(v), nil
}

// landlockAvailable is the startup probe. It asks for the ABI and then builds a
// real ruleset with the rights the policy uses, because a mechanism that is
// claimed and cannot run is worse than one that is absent — the interface shows
// a boundary that is not there, and every tool fails on launch.
func landlockAvailable() (bool, string) {
	abi, err := landlockABI()
	if err != nil {
		if err == syscall.ENOSYS {
			return false, "this kernel has no Landlock; it is in Linux 5.13 and later"
		}
		return false, "Landlock is not available here: " + err.Error()
	}
	if abi < 1 {
		return false, "Landlock is compiled in but disabled; add it to the kernel's lsm= list"
	}
	fd, err := createRuleset(handledFor(abi))
	if err != nil {
		return false, "Landlock refused a ruleset here: " + err.Error()
	}
	syscall.Close(fd)
	return true, ""
}

func createRuleset(handled uint64) (int, error) {
	attr := rulesetAttr{HandledAccessFS: handled}
	fd, _, errno := syscall.Syscall(sysLandlockCreateRuleset,
		uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return 0, errno
	}
	return int(fd), nil
}

// addRule grants access beneath one path.
//
// The path is opened with O_PATH, which resolves it without opening what is
// there: /dev/tty is a device that cannot be opened when there is no controlling
// terminal, and a rule about it should not depend on being able to talk to it.
// struct landlock_path_beneath_attr is packed — a u64 followed by an s32 — so
// the bytes are laid out by hand rather than left to Go's alignment.
func addRule(rulesetFD int, path string, access uint64) error {
	fd, err := syscall.Open(path, oPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer syscall.Close(fd)
	var attr [12]byte
	*(*uint64)(unsafe.Pointer(&attr[0])) = access
	*(*int32)(unsafe.Pointer(&attr[8])) = int32(fd)
	_, _, errno := syscall.Syscall6(sysLandlockAddRule, uintptr(rulesetFD),
		landlockRuleTypePathBeneath, uintptr(unsafe.Pointer(&attr[0])), 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("%s: %w", path, errno)
	}
	return nil
}

// applyPolicy restricts this thread, and therefore the program it is about to
// become. A path that is not granted is denied: there is no rule that permits
// the rest, which is what makes this an allow-list rather than a set of
// exceptions to a permission.
func applyPolicy(p policy) error {
	// The restriction belongs to the thread that asks for it, and execve must
	// happen on that same thread.
	runtime.LockOSThread()

	abi, err := landlockABI()
	if err != nil || abi < 1 {
		return fmt.Errorf("no Landlock on this kernel: %v", err)
	}
	handled := handledFor(abi)
	fd, err := createRuleset(handled)
	if err != nil {
		return fmt.Errorf("landlock ruleset: %w", err)
	}
	defer syscall.Close(fd)

	for _, path := range p.Write {
		if err := addRule(fd, path, handled); err != nil {
			return err
		}
	}
	// A path that is not there is skipped rather than refused: the runtime
	// differs between a container and a laptop, and the policy names what may be
	// read rather than what must exist. The working directory above is not
	// optional, which is why its failures are.
	for _, path := range p.Read {
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		if err := addRule(fd, path, readRights); err != nil {
			return err
		}
	}
	for _, path := range p.Files {
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		access := uint64(fileRights)
		if abi >= 3 {
			access |= fsTruncate
		}
		if err := addRule(fd, path, access); err != nil {
			return err
		}
	}

	// Without no_new_privs the kernel refuses to restrict an unprivileged
	// process, because a setuid program could otherwise escape the domain.
	if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return fmt.Errorf("no_new_privs: %w", errno)
	}
	if _, _, errno := syscall.Syscall(sysLandlockRestrictSelf, uintptr(fd), 0, 0); errno != 0 {
		return fmt.Errorf("landlock restrict: %w", errno)
	}
	return nil
}
