package app

import (
	"encoding/json"
	"fmt"
	"os"
	"syscall"
)

// The confinement a tool runs under, as the wrapper receives it.
//
// On Linux the agent is its own sandbox wrapper. Landlock restricts the thread
// that asks and is inherited across execve, so something has to ask between the
// fork and the exec — and Go gives no hook there. The agent therefore re-runs
// itself: `agent -confine <policy> -- <tool>`, and that child restricts itself
// and then becomes the tool. It is the same shape as sandbox-exec and bwrap, a
// command in front of a command, with no second binary to ship.
type policy struct {
	// Write is where a tool may write, and read: its own working directory.
	Write []string `json:"w"`
	// Read is the runtime, the tool directory, and whatever the operator named.
	Read []string `json:"r"`
	// Files is read and write on named files rather than trees — the database
	// and its journals, which a tool is handed on purpose.
	Files []string `json:"f"`
	// Chdir is where the tool starts, applied before the restriction so a
	// working directory outside the policy is still refused by it.
	Chdir string `json:"cd,omitempty"`
}

func (p policy) encode() string {
	b, _ := json.Marshal(p)
	return string(b)
}

// Confine is `agent -confine <policy> -- <cmd> <args...>`. It applies the
// policy to itself and execs the command, replacing this process: a failure
// here must never fall through to running the command unconfined, so every
// error path exits.
func Confine(spec string, argv []string) {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "confine: nothing to run")
		os.Exit(2)
	}
	var p policy
	if err := json.Unmarshal([]byte(spec), &p); err != nil {
		fmt.Fprintln(os.Stderr, "confine: unreadable policy:", err)
		os.Exit(2)
	}
	if p.Chdir != "" {
		if err := os.Chdir(p.Chdir); err != nil {
			fmt.Fprintln(os.Stderr, "confine:", err)
			os.Exit(2)
		}
	}
	if err := applyPolicy(p); err != nil {
		fmt.Fprintln(os.Stderr, "confine:", err)
		os.Exit(2)
	}
	if err := syscall.Exec(argv[0], argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "confine: %s: %v\n", argv[0], err)
		os.Exit(2)
	}
}
