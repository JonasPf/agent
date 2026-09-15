// Command eval puts each tool's eval cases to a real model and reports what it
// did with them. It costs money and its results vary between runs, so it is a
// command rather than part of go test.
//
//	go run ./cmd/eval                     every tool
//	go run ./cmd/eval schedule            one tool
//	go run ./cmd/eval -model <id> read    a model other than the default
//
// The model is a flag and not a setting. It configures this command, not the
// agent, and the agent never reads it — offering it in .env would put it in a
// file the agent parses at startup and then ignores.
package main

import (
	"flag"
	"fmt"
	"os"

	"agent/internal/app"
)

func main() {
	model := flag.String("model", app.DefaultEvalModel,
		"model to put the cases to; any id the gateway lists")
	confine := flag.String("confine", "",
		"internal: restrict this process to the given policy and become the command after --")
	flag.Parse()

	// The sandbox wrapper is whichever program is running, and here that is this
	// one: where Landlock is enforced, every tool a case calls is run as
	// `eval -confine <policy> -- <tool>`, and this is that second run. It never
	// returns — it becomes the tool, or it exits.
	if *confine != "" {
		app.Confine(*confine, flag.Args())
		return
	}

	if err := app.RunEvals(*model, flag.Args(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}
