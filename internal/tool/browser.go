package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Most of the web arrives empty: the HTML a server sends is a shell, and the
// text a person reads is written into it by scripts once the page is running.
// Reading it therefore means running it, which means a browser.

// browsers is where a Chrome-family browser usually lives. AGENT_BROWSER
// overrides the lot.
var browsers = []string{
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
	"/usr/bin/google-chrome",
	"/usr/bin/google-chrome-stable",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
}

// PlaywrightBrowsers lists the Chromium builds Playwright has downloaded,
// newest first. The headless shell comes before the full application: a full
// browser bundle does not start under the sandbox, and the shell does.
func PlaywrightBrowsers() []string {
	root := os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		if runtime.GOOS == "darwin" {
			root = filepath.Join(home, "Library", "Caches", "ms-playwright")
		} else {
			root = filepath.Join(home, ".cache", "ms-playwright")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "chromium") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var found []string
	for _, name := range names {
		for _, p := range []struct{ glob, exe string }{
			{"chrome-headless-shell-*", "chrome-headless-shell"},
			{"chrome-*", "chrome"},
			{"chrome-*", "Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"},
		} {
			dirs, _ := filepath.Glob(filepath.Join(root, name, p.glob))
			sort.Strings(dirs)
			for _, d := range dirs {
				found = append(found, filepath.Join(d, p.exe))
			}
		}
	}
	return found
}

// Exists reports whether a path is there. It is a parameter of PickBrowser so
// the choice can be tested without a browser installed.
func Exists(p string) bool { _, err := os.Stat(p); return err == nil }

// PickBrowser resolves the browser to drive.
//
// An explicit AGENT_BROWSER that does not exist is an error rather than a
// fallback: a setting that is silently ignored is worse than one that fails.
func PickBrowser(env string, candidates []string, exists func(string) bool) (string, error) {
	if env != "" {
		if !exists(env) {
			return "", fmt.Errorf("AGENT_BROWSER is set to %s, which does not exist", env)
		}
		return env, nil
	}
	for _, c := range candidates {
		if exists(c) {
			return c, nil
		}
	}
	return "", nil
}

// Browser is PickBrowser over the real environment.
func Browser() (string, error) {
	return PickBrowser(os.Getenv("AGENT_BROWSER"),
		append(PlaywrightBrowsers(), browsers...), Exists)
}

// Render returns the DOM of url after the page has loaded and run its scripts.
//
// The browser is used as it comes. Nothing here disguises the request or works
// around a site that declines to serve it.
func Render(url string, seconds int) string {
	browser, err := Browser()
	if err != nil {
		Failf("%s", err)
	}
	if browser == "" {
		Failf("no headless browser found. Install Chromium, or set AGENT_BROWSER " +
			"to a Chrome-family executable. It must sit inside a path the sandbox " +
			"allows a tool to read — see AGENT_READ_PATHS.")
	}
	// A browser insists on a profile directory it can write. The system one is
	// outside what a tool may touch, so it goes in the session's own directory
	// alongside everything else the tool writes.
	profile := filepath.Join(envOr("TMPDIR", Workspace), "browser-profile")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		Failf("could not make a browser profile directory: %v", err)
	}
	cmd := exec.Command(browser, "--headless", "--disable-gpu", "--no-sandbox",
		"--user-data-dir="+profile,
		fmt.Sprintf("--virtual-time-budget=%d", seconds*1000),
		"--dump-dom", url)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		Failf("could not start %s: %v", browser, err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Duration(seconds+20) * time.Second):
		_ = cmd.Process.Kill()
		Failf("%s did not finish loading within %ds", url, seconds+20)
	}
	page := out.String()
	if strings.TrimSpace(page) == "" {
		Failf("%s rendered nothing (%s)", url, truncate(strings.TrimSpace(errb.String()), 200))
	}
	return page
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
