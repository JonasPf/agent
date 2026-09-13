package app

import (
	"os"
	"testing"
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
	os.Exit(m.Run())
}
