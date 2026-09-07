// Command eval puts each tool's eval cases to a real model and reports what it
// did with them. It costs money and its results vary between runs, so it is a
// command rather than part of go test.
//
//	go run ./cmd/eval            every tool
//	go run ./cmd/eval schedule   one tool
//
// AGENT_EVAL_MODEL selects the model; it defaults to a free one.
package main

import (
	"fmt"
	"os"

	"agent/internal/app"
)

func main() {
	if err := app.RunEvals(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}
