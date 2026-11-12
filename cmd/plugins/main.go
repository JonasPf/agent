// Command plugins fetches the bundles and skills plugins.lock names, each into
// a folder of its own and each checked against the sha256 the lock gives. The image is built with it, and
// the tests and evals run against what it fetched.
//
//	go run ./cmd/plugins -out plugins
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"agent/internal/app"
)

func main() {
	lock := flag.String("lock", "plugins.lock", "the lock file")
	out := flag.String("out", "plugins", "where the bundles are unpacked, one folder each")
	skills := flag.String("skills", "skills", "where the skills are unpacked, one folder each")
	arch := flag.String("arch", runtime.GOARCH, "the architecture to fetch for: amd64 or arm64")
	flag.Parse()
	if err := app.FetchPlugins(*lock, *out, *skills, *arch, nil, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "plugins:", err)
		os.Exit(1)
	}
}
