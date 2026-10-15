package app

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The API is documented in one file, specs/openapi.yaml, which the spec's API
// page renders as it stands. It used to be two — openapi.json and a copy of it
// in openapi.js for the page — kept in step by hand, and each had drifted its
// own way: one still described an endpoint removed with rotation, the other
// lacked comparing and compacting, and neither knew the files endpoints. So
// the routes the agent registers are read from its source and checked against
// the document both ways, the way .env.example is checked against the
// variables the agent reads.

var routePattern = regexp.MustCompile(`mux\.HandleFunc\("(?:(GET|POST|PUT|PATCH|DELETE) )?([^"]+)"`)

// registeredRoutes is every method and path routes() registers. A route with
// no method is the WebSocket, which a client opens with a GET.
func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile("http.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func (a *App) routes()")
	end := strings.Index(body[start:], "\n}\n")
	out := map[string]bool{}
	for _, m := range routePattern.FindAllStringSubmatch(body[start:start+end], -1) {
		method := strings.ToLower(m[1])
		if method == "" {
			method = "get"
		}
		path := strings.ReplaceAll(m[2], "...}", "}")
		if path == "/" {
			continue // the interface's own files, served from disk
		}
		out[method+" "+path] = true
	}
	return out
}

// documentedOperations is every method and path under paths: in the document.
// The file is written in block style, a path two spaces in and a method four,
// which is all this reads.
func documentedOperations(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "specs", "openapi.yaml"))
	if err != nil {
		t.Fatalf("the API document is missing: %v", err)
	}
	defer f.Close()
	path := regexp.MustCompile(`^  (/[^:]*):\s*$`)
	method := regexp.MustCompile(`^    (get|post|put|patch|delete):`)
	out := map[string]bool{}
	in, current := false, ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, " ") && strings.TrimSpace(line) != "" {
			in = strings.HasPrefix(line, "paths:")
			continue
		}
		if !in {
			continue
		}
		if m := path.FindStringSubmatch(line); m != nil {
			current = m[1]
		} else if m := method.FindStringSubmatch(line); m != nil && current != "" {
			out[m[1]+" "+current] = true
		}
	}
	return out
}

func TestEveryRouteIsDocumentedAndEveryDocumentedRouteExists(t *testing.T) {
	routes, docs := registeredRoutes(t), documentedOperations(t)
	if len(routes) < 40 {
		t.Fatalf("read only %d routes from http.go; the scan no longer matches how routes are registered", len(routes))
	}
	var missing, stale []string
	for r := range routes {
		if !docs[r] {
			missing = append(missing, r)
		}
	}
	for d := range docs {
		if !routes[d] {
			stale = append(stale, d)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	for _, r := range missing {
		t.Errorf("%s is served but not in specs/openapi.yaml", r)
	}
	for _, d := range stale {
		t.Errorf("%s is in specs/openapi.yaml but nothing serves it", d)
	}
}

// One document, rendered directly: a second copy is a second thing to drift.
func TestTheAPIIsDocumentedOnce(t *testing.T) {
	for _, copyOf := range []string{"openapi.json", "openapi.js"} {
		if _, err := os.Stat(filepath.Join("..", "..", "specs", copyOf)); err == nil {
			t.Errorf("specs/%s is a second copy of the API document; specs/openapi.yaml is the one", copyOf)
		}
	}
	page, err := os.ReadFile(filepath.Join("..", "..", "specs", "api.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "openapi.yaml") {
		t.Error("the API page does not render specs/openapi.yaml")
	}
}
