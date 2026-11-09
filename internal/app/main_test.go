package app

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

// On Linux the sandbox wrapper is the agent's own binary, re-run in front of
// every tool: it restricts itself to a policy and then becomes the tool. Under
// test the binary in front is this one, so it has to answer -confine the same
// way — which means the confinement these tests exercise is the code that runs
// in production, not a stand-in for it.
//
// This runs before the testing flags are parsed, which is why it can accept a
// flag the test binary otherwise knows nothing about.
func TestMain(m *testing.M) {
	if len(os.Args) > 4 && os.Args[1] == "-confine" && os.Args[3] == "--" {
		Confine(os.Args[2], os.Args[4:]) // never returns
	}
	// The same trick for the network boundary: a confined copy of this binary
	// opens one TCP connection and reports whether the kernel let it.
	if len(os.Args) == 3 && os.Args[1] == "-dial" {
		conn, err := net.DialTimeout("tcp", os.Args[2], 3*time.Second)
		if err != nil {
			fmt.Println("refused:", err)
			os.Exit(3)
		}
		conn.Close()
		fmt.Println("connected")
		os.Exit(0)
	}
	// And for btrfs: a copy run as a user who is not root snapshots and
	// deletes, as the deployed agent has to.
	if len(os.Args) == 3 && os.Args[1] == "-btrfs-as-user" {
		if err := btrfsAsUser(os.Args[2]); err != nil {
			fmt.Println("failed:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	// Every agent these tests build lives under the temporary directory, and
	// the agent keeps conversations' files only on btrfs (ADR-082). Anywhere
	// else every test would fail on its own, a hundred times over, without
	// saying why; this says it once.
	if ok, err := onBtrfs(os.TempDir()); !ok {
		fmt.Fprintf(os.Stderr, "the temporary directory %s is not btrfs (%v): the agent snapshots "+
			"conversations' files on btrfs and refuses anything else (ADR-082). Run the tests "+
			"with `task test`, which mounts one, or point TMPDIR at a btrfs filesystem mounted "+
			"with user_subvol_rm_allowed.\n", os.TempDir(), err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
