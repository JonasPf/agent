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
	flag.Parse()
	if err := app.RunEvals(*model, flag.Args(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}
