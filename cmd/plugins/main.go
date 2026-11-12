// Command plugins fetches the bundles plugins.lock names into a folder, each
// checked against the sha256 the lock gives. The image is built with it, and
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
	arch := flag.String("arch", runtime.GOARCH, "the architecture to fetch for: amd64 or arm64")
	flag.Parse()
	if err := app.FetchPlugins(*lock, *out, *arch, nil, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "plugins:", err)
		os.Exit(1)
	}
}
