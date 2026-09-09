//go:build linux

package app

import "syscall"

// prSetDumpable: a process that is not dumpable has its /proc entries owned by
// root, and no other process of the same user may read them.
const prSetDumpable = 4

// hideProcess takes the agent's own environment out of reach. A tool that is
// granted /proc — the browser tools are, because a browser reads its own maps
// before it renders anything — could otherwise read /proc/<agent>/environ and
// find the model key there, whatever ADR-037 keeps out of the tool's own
// environment. Landlock cannot grant a tree while denying part of it, so the
// answer is not to deny the path but to remove what is behind it.
//
// It costs one syscall at startup and a core dump nobody wanted.
func hideProcess() error {
	if _, _, errno := syscall.Syscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
