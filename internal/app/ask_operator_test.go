package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A file in a working directory is whatever the agent wrote or fetched, so it
// is shown, never trusted: a page opened from one runs with no origin, and no
// way to reach the routes the operator's login opens.
func TestAPageInAWorkingDirectoryOpensWithoutTheOperatorsAuthority(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	for _, name := range []string{"page.html", "drawing.svg", "no-extension"} {
		if w := upload(t, a, s.ID, name, "<html><script>fetch('/skills')</script></html>"); w.Code != 201 {
			t.Fatalf("upload %s = %d", name, w.Code)
		}
		h := get(t, a, "/sessions/"+s.ID+"/files/"+name, false).Header()
		csp := h.Get("Content-Security-Policy")
		if !strings.Contains(csp, "sandbox allow-scripts") || strings.Contains(csp, "allow-same-origin") {
			t.Errorf("%s: Content-Security-Policy = %q, want a sandbox without the agent's origin", name, csp)
		}
		if !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: Content-Security-Policy = %q, want nothing loaded from anywhere", name, csp)
		}
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", name, got)
		}
	}
}

// A browser refuses to show a PDF in a sandbox, and shows one without running
// it on this origin, so a PDF keeps opening.
func TestAPDFInAWorkingDirectoryStillOpens(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	if w := upload(t, a, s.ID, "report.pdf", "%PDF-1.7"); w.Code != 201 {
		t.Fatalf("upload = %d", w.Code)
	}
	h := get(t, a, "/sessions/"+s.ID+"/files/report.pdf", false).Header()
	if got := h.Get("Content-Security-Policy"); got != "" {
		t.Errorf("a PDF was sent with Content-Security-Policy %q", got)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
}

func callAsk(t *testing.T, a *App, sid string, args map[string]any) toolResult {
	t.Helper()
	b, _ := json.Marshal(args)
	return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: sid}, "ask_operator", b)
}

var askLink = regexp.MustCompile(`#toolpanel/ask_operator/([A-Za-z0-9]+)/([0-9a-z-]+)`)

// The tool as the registry runs it: a real subprocess in its session's directory.
func TestAskOperatorRecordsTheQuestionsAndReturnsALink(t *testing.T) {
	a := browseApp(t)
	s := newSession(t, a)
	res := callAsk(t, a, s.ID, map[string]any{
		"message": "Which voice should read the demo?",
		"questions": []map[string]any{
			{"name": "voice", "type": "choice", "options": []string{"gareth", "brian"}, "required": true},
		},
	})
	if !res.OK {
		t.Fatalf("ask_operator: %s", res.Error)
	}
	m := askLink.FindStringSubmatch(res.Content)
	if m == nil || m[1] != s.ID {
		t.Fatalf("no link to this conversation in %q", res.Content)
	}
	raw, err := os.ReadFile(filepath.Join(a.sessionWorkspace(s.ID), "asks", m[2]+".json"))
	if err != nil {
		t.Fatalf("the ask was not recorded: %v", err)
	}
	var got struct {
		Message   string `json:"message"`
		Questions []struct {
			Name string `json:"name"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Message != "Which voice should read the demo?" || len(got.Questions) != 1 || got.Questions[0].Name != "voice" {
		t.Errorf("recorded %s", raw)
	}
}

func TestAskOperatorRefusesWhatItCannotShow(t *testing.T) {
	a := browseApp(t)
	s := newSession(t, a)
	dir := a.ensureWorkspace(s.ID)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a page"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("<p>hi</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	one := []map[string]any{{"name": "a", "type": "text"}}
	for name, args := range map[string]map[string]any{
		"no message":        {"questions": one},
		"nothing to answer": {"message": "Well?"},
		"both kinds":        {"message": "Well?", "questions": one, "page": "page.html"},
		"a missing page":    {"message": "Well?", "page": "gone.html"},
		"a page elsewhere":  {"message": "Well?", "page": "../other/page.html"},
		"not a page":        {"message": "Well?", "page": "notes.txt"},
		"an unknown type":   {"message": "Well?", "questions": []map[string]any{{"name": "a", "type": "slider"}}},
		"a choice of none":  {"message": "Well?", "questions": []map[string]any{{"name": "a", "type": "choice"}}},
		"a repeated name":   {"message": "Well?", "questions": []map[string]any{{"name": "a", "type": "text"}, {"name": "a", "type": "text"}}},
	} {
		if res := callAsk(t, a, s.ID, args); res.OK {
			t.Errorf("%s was accepted: %s", name, res.Content)
		}
	}
}

// What a page saves arrives through the panel, over the API, and stays beside
// the page: a page has no business rewriting the rest of the working directory.
func TestAPageSavesOnlyBesideItself(t *testing.T) {
	a := browseApp(t)
	s := newSession(t, a)
	dir := a.ensureWorkspace(s.ID)
	if err := os.MkdirAll(filepath.Join(dir, "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review", "index.html"), []byte("<p>hi</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := callAsk(t, a, s.ID, map[string]any{"message": "Check the script.", "page": "review/index.html"})
	if !res.OK {
		t.Fatalf("ask_operator: %s", res.Error)
	}
	id := askLink.FindStringSubmatch(res.Content)[2]

	save := func(name string) toolResult {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"ask": id, "save": name, "content": `{"ok":true}`})
		r := httptest.NewRequest("POST", "/tools/ask_operator/call?session_id="+s.ID, strings.NewReader(string(b)))
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		var out toolResult
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%d: %s", w.Code, w.Body)
		}
		return out
	}
	if res := save("chunks.json"); !res.OK {
		t.Fatalf("saving beside the page: %s", res.Error)
	}
	got, err := os.ReadFile(filepath.Join(dir, "review", "chunks.json"))
	if err != nil || string(got) != `{"ok":true}` {
		t.Errorf("review/chunks.json = %q, %v", got, err)
	}
	for _, name := range []string{"../escaped.json", "/etc/passwd", "../../other.json"} {
		if res := save(name); res.OK {
			t.Errorf("saving %q was allowed: %s", name, res.Content)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.json")); err == nil {
		t.Error("a page wrote outside its own folder")
	}
}

// heldOpen is the real interface with one thing added, for a test's browser. A
// headless browser's clock runs ahead of real time and it reports the page when
// that clock runs out, which can be before a page in a box has finished, because
// a frame's scripts do not stop the clock. A request in flight does, so the panel
// keeps one going until the page's answer is on the screen.
func heldOpen(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	panel, err := os.ReadFile("../../tools/ask_operator/ui/panel.js")
	if err != nil {
		t.Fatal(err)
	}
	const hold = `
;(function hold(left) {
  const shown = document.querySelector('.ask-proposal');
  if (left && (!shown || shown.hidden)) fetch('/held').then(() => hold(left - 1));
})(200);`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/held":
			time.Sleep(25 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		case "/tools/ask_operator/panel.js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(panel)
			_, _ = w.Write([]byte(hold))
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// The whole of the promise, in a real browser against the real interface: the
// page runs, it can reach the panel, it can save beside itself, it cannot reach
// the agent, and nothing it does reaches the conversation by itself.
func TestAPageRunsInABoxAndCannotAnswerForTheOperator(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	a.or = NewOpenRouter("")
	a.or.base = "http://127.0.0.1:1" // the status screen asks; nothing here needs an answer
	srv := httptest.NewServer(heldOpen(t, a.routes()))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)

	s := newSession(t, a)
	if _, err := a.store.UpdateSession(s.ID, func(x *Session) { x.Title = "Sign-up mockup" }); err != nil {
		t.Fatal(err)
	}
	dir := a.ensureWorkspace(s.ID)
	if err := os.MkdirAll(filepath.Join(dir, "mock"), 0o755); err != nil {
		t.Fatal(err)
	}
	page := `<!doctype html><html><body><p id="seen">page ran</p><script>
		fetch('` + srv.URL + `/skills', {method: 'POST', mode: 'no-cors', body: '{}'})
			.then(function () { return 'reached the agent'; }, function () { return 'blocked'; })
			.then(function (r) {
				return ask.save('notes.txt', 'from the page').then(function (where) {
					ask.answer('fetch was ' + r + ', saved ' + where);
				});
			}).catch(function (e) { ask.answer('failed: ' + e.message); });
	</script></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "mock", "page.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	res := callAsk(t, a, s.ID, map[string]any{"message": "Look at this mockup.", "page": "mock/page.html"})
	if !res.OK {
		t.Fatalf("ask_operator: %s", res.Error)
	}
	link := askLink.FindString(res.Content)
	before := len(a.store.Entries(s.ID))

	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/" + link})
	shown := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !shown.OK {
		t.Fatalf("web_browse: %s", shown.Error)
	}
	if !strings.Contains(shown.Content, "Sign-up mockup asks:") {
		t.Errorf("the panel does not say which conversation is asking:\n%s", shown.Content)
	}
	if !strings.Contains(shown.Content, "Look at this mockup.") {
		t.Errorf("the panel does not say why it is asking:\n%s", shown.Content)
	}
	if !strings.Contains(shown.Content, "fetch was blocked") {
		t.Errorf("the page's fetch was not blocked, or its answer did not reach the panel:\n%s", shown.Content)
	}
	if !strings.Contains(shown.Content, "saved mock/notes.txt") {
		t.Errorf("the page's save did not come back:\n%s", shown.Content)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "mock", "notes.txt")); err != nil || string(got) != "from the page" {
		t.Errorf("mock/notes.txt = %q, %v", got, err)
	}
	if got := len(a.store.Entries(s.ID)); got != before {
		t.Errorf("the page reached the conversation: %d entries, was %d", got, before)
	}
}

// The same promise for a page opened straight from the Files screen, with no
// panel around it: its scripts run, and it reaches nothing.
func TestAPageOpenedFromFilesRunsAndCannotReachTheAgent(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	s := newSession(t, a)
	page := `<!doctype html><html><body><p id="out">not run</p><script>
		fetch('` + srv.URL + `/skills', {method: 'POST', mode: 'no-cors', body: '{}'}).then(
			function () { document.getElementById('out').textContent = 'reached the agent'; },
			function () { document.getElementById('out').textContent = 'stayed in the box'; });
	</script></body></html>`
	if err := os.WriteFile(filepath.Join(a.ensureWorkspace(s.ID), "mock.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/sessions/" + s.ID + "/files/mock.html"})
	shown := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !shown.OK {
		t.Fatalf("web_browse: %s", shown.Error)
	}
	if !strings.Contains(shown.Content, "stayed in the box") {
		t.Errorf("the page did not run, or reached the agent:\n%s", shown.Content)
	}
}
