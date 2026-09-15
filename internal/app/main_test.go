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
	os.Exit(m.Run())
}
