package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const appsManifest = `{
  "manifest_version": "0.3", "name": "apps", "version": "0.0.1",
  "description": "A bundle with a page, for the tests.", "author": {"name": "the tests"},
  "server": {"type": "binary", "entry_point": "server/shell",
    "mcp_config": {"command": "${__dirname}/server/shell", "args": ["apps"]}}
}`

// appsApp is a confined agent holding the bundle with a page and the plain
// shell bundle beside it, and a conversation in which the model called show.
func appsApp(t *testing.T, args map[string]string) (*App, *Session, Entry) {
	t.Helper()
	bundles := t.TempDir()
	writeBundle(t, bundles, "apps", appsManifest)
	writeBundle(t, bundles, "shell", shellManifest)
	a, failures := bundleApp(t, bundles)
	if len(failures) > 0 {
		t.Fatalf("the bundles did not load: %+v", failures)
	}
	raw, _ := json.Marshal(args)
	srv := scriptedModel(t, toolStep{tool: "show", args: string(raw)}, toolStep{text: "Shown."})
	a.or.base = srv.URL
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err := a.SendUserMessage(s.ID, "show me"); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)
	for _, e := range a.store.Entries(s.ID) {
		if e.Role == "tool" && e.ToolName == "show" {
			return a, s, e
		}
	}
	t.Fatal("show was not called")
	return nil, nil, Entry{}
}

func appPath(s *Session, e Entry) string {
	return "/sessions/" + s.ID + "/apps/" + strconv.Itoa(e.Seq)
}

// "A tool that only a page may call is not sent to the model", and "the result
// of a tool with a page is kept whole for the page, and the model is sent its
// text only".
func TestAToolWithAPageKeepsItsResultForThePageAndItsTextForTheModel(t *testing.T) {
	a, s, shown := appsApp(t, map[string]string{"note": "hello"})

	sent := map[string]bool{}
	for _, schema := range a.tools.SchemasFor(s) {
		sent[schema.Function.Name] = true
	}
	if !sent["show"] || !sent["quiet"] || sent["refresh"] {
		t.Errorf("the model is sent %v, want show and quiet and not refresh", sent)
	}
	if strings.Contains(string(shown.ToolResult), "42") || !strings.Contains(string(shown.ToolResult), "The page is shown.") {
		t.Errorf("the model is sent %s, want the text and not what is for the page", shown.ToolResult)
	}

	w := callAPI(t, a, "GET", appPath(s, shown), nil)
	if w.Code != 200 {
		t.Fatalf("GET app = %d %s", w.Code, w.Body)
	}
	var app struct {
		HTML string `json:"html"`
		Tool struct {
			Name string `json:"name"`
		} `json:"tool"`
		Arguments map[string]any `json:"arguments"`
		Result    struct {
			StructuredContent map[string]any `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(app.HTML, `id="state"`) {
		t.Errorf("the page is not the bundle's: %.80s", app.HTML)
	}
	if app.Tool.Name != "show" || app.Arguments["note"] != "hello" {
		t.Errorf("tool %q arguments %v, want the call that opened the page", app.Tool.Name, app.Arguments)
	}
	if app.Result.StructuredContent["value"] != 42.0 || len(app.Result.Content) != 1 || app.Result.Content[0].Text != "The page is shown." {
		t.Errorf("result = %+v, want the whole result", app.Result)
	}

	// An entry that opened no page, and one that is not there.
	if w := callAPI(t, a, "GET", "/sessions/"+s.ID+"/apps/1", nil); w.Code != 404 {
		t.Errorf("an entry with no page = %d, want 404", w.Code)
	}
	if w := callAPI(t, a, "GET", "/sessions/"+s.ID+"/apps/9999", nil); w.Code != 404 {
		t.Errorf("no such entry = %d, want 404", w.Code)
	}
}

// "A page calls the tools of its own bundle that allow it, in its
// conversation's directory, and each call is on the transcript; any other tool
// is refused."
func TestAPageCallsOnlyItsOwnBundlesToolsAndEachCallIsRecorded(t *testing.T) {
	a, s, shown := appsApp(t, map[string]string{"note": "hello"})
	before := len(a.store.Entries(s.ID))

	w := sendJSON(t, a, "POST", appPath(s, shown)+"/call", map[string]any{"name": "refresh", "arguments": map[string]any{}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"fresh"`) {
		t.Fatalf("calling refresh = %d %s", w.Code, w.Body)
	}
	if got, err := os.ReadFile(filepath.Join(a.ensureWorkspace(s.ID), "refreshed.txt")); err != nil || !strings.Contains(string(got), "by the page") {
		t.Errorf("the call did not run in the conversation's directory: %q %v", got, err)
	}
	entries := a.store.Entries(s.ID)
	if len(entries) != before+1 {
		t.Fatalf("%d entries after the call, was %d; want the call recorded once", len(entries), before)
	}
	last := entries[len(entries)-1]
	if last.Type != "event" || last.EventKind != "app_call" || !strings.Contains(last.Text, "refresh") {
		t.Errorf("recorded %+v, want an event naming the tool", last)
	}
	if _, sent := toMessage(last); sent {
		t.Error("the record of a page's call is sent to the model")
	}

	for name, why := range map[string]string{
		"quiet":         "a tool for the model only",
		"sh":            "another bundle's tool",
		"skill_read":    "a built-in",
		"no_such_thing": "no tool at all",
	} {
		w := sendJSON(t, a, "POST", appPath(s, shown)+"/call", map[string]any{"name": name, "arguments": map[string]any{"command": "echo hi"}})
		if w.Code != 403 {
			t.Errorf("%s (%s) = %d %s, want 403", name, why, w.Code, w.Body)
		}
	}
	if got := len(a.store.Entries(s.ID)); got != before+1 {
		t.Errorf("a refused call was recorded or run: %d entries", got)
	}
}

// The whole of it in a real browser against the real interface: the page
// opens from the transcript's link, is handed the call's input and result,
// calls its bundle's tool, is refused a tool that is not for it, cannot reach
// the agent, and what it sends reaches the conversation as a message.
func TestAnAppsPageRunsInABoxInARealBrowser(t *testing.T) {
	browserOrSkip(t)
	bundles := t.TempDir()
	writeBundle(t, bundles, "apps", appsManifest)
	a := browseApp(t)
	a.tools.bundleDirs = append(a.tools.bundleDirs, bundles)
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("the bundles did not load: %+v", failures)
	}
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "The page sent"))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)

	raw, _ := json.Marshal(map[string]string{"note": "hello", "probe": srv.URL + "/skills"})
	model := scriptedModel(t, toolStep{tool: "show", args: string(raw)}, toolStep{text: "Shown."})
	a.or = NewOpenRouter("test-key")
	a.or.base = model.URL
	s := newSession(t, a)
	if err := a.SendUserMessage(s.ID, "show me"); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)
	var shown Entry
	for _, e := range a.store.Entries(s.ID) {
		if e.Role == "tool" && e.ToolName == "show" {
			shown = e
		}
	}

	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/#app/" + s.ID + "/" + strconv.Itoa(shown.Seq)})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	settle(t, a, s.ID)
	want := "value=42 note=hello refresh=fresh quiet=refused fetch=blocked"
	if !strings.Contains(seen.Content, "The page sent") {
		t.Errorf("the screen does not say the page sent a message:\n%s", seen.Content)
	}
	said := ""
	for _, e := range a.store.Entries(s.ID) {
		if e.Type == "message" && e.Role == "user" && strings.Contains(e.Text, "value=") {
			said = e.Text
		}
	}
	if said != want {
		t.Errorf("the conversation was sent %q, want %q", said, want)
	}
}

// heldUntil is the real interface with one thing added, for a test's browser:
// a request kept in flight until the screen says text, so the headless
// browser, whose clock a frame's scripts do not hold, waits for the page in
// the box to finish (see heldOpen).
func heldUntil(t *testing.T, next http.Handler, text string) http.Handler {
	t.Helper()
	script, err := os.ReadFile("../../web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	hold := `
;(function hold(left) {
  if (left && !document.body.textContent.includes(` + strconv.Quote(text) + `)) fetch('/held').then(() => hold(left - 1));
})(200);`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/held":
			time.Sleep(25 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(script)
			_, _ = w.Write([]byte(hold))
		default:
			next.ServeHTTP(w, r)
		}
	})
}
