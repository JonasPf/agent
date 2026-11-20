package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// statesProbe writes whether the screen was marked busy as the page first
// drew it, then waits for what the screen shows in place of what it would
// hold and writes its heading, its buttons, and whether it is still marked
// busy. A screen that did not load is told to heal and tried again from its
// own button, and what it then shows is written too.
//
// One the agent's proxy answered for is left to load by itself, which it does
// on a timer. The browser's clock is virtual and stands still while a request
// is pending, as one always is while the page is held open; so the probe
// writes what it has, which releases the hold, and waits on timers instead.
const statesProbe = `(function probe(left, seen, healed) {
  const view = document.getElementById('view');
  const shown = () => {
    const b = view.querySelector('.blank');
    const title = b ? b.querySelector('h2').textContent : 'nothing';
    const buttons = b ? [...b.querySelectorAll('button')].map(x => x.textContent).join('+') : '';
    return { b, title, buttons, line: title + ':' + buttons + ' busy=' + view.getAttribute('aria-busy') };
  };
  const out = document.createElement('pre');
  const write = () => { out.textContent = 'states: ' + seen.join(' ; ') + ' |'; document.body.prepend(out); };
  if (!seen.length) seen.push('busy=' + view.getAttribute('aria-busy') + ',' + !document.getElementById('busy').hidden);
  const now = shown();
  const ready = healed ? now.title === 'No jobs scheduled' : !!now.b;
  if (left && !ready) { fetch('/held').then(() => probe(left - 1, seen, healed)); return; }
  seen.push(now.line);
  if (healed || now.buttons !== 'Try again') return write();
  seen.push('says=' + now.b.querySelector('p').textContent);
  if (now.title !== 'The agent is not answering') {
    fetch('/heal').then(() => { now.b.querySelector('button').click(); probe(left, seen, true); });
    return;
  }
  write();
  fetch('/heal').then(() => (function wait(n) {
    const then = shown();
    if (n && then.title !== 'No jobs scheduled') { setTimeout(() => wait(n - 1), 250); return; }
    seen.push(then.line);
    write();
  })(56));
})(200, [], false);`

// A screen says it is busy while it waits on the agent, and when its answers
// do not come says which way they did not — the conversation is gone, the
// agent refused, the agent could not be reached — and offers the way on; an
// empty list says what would fill it (webui.html, "States"). In a real browser
// against the interface as shipped, with the agent's answer to the jobs screen
// refused, its connection dropped, or a proxy's page in its place, until the
// page asks for it to heal.
func TestScreensSayWhyTheyHoldNothing(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	var mu sync.Mutex
	mode := ""
	held := heldUntil(t, a.routes(), "states:", statesProbe)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		how := mode
		if r.URL.Path == "/heal" {
			mode = ""
		}
		mu.Unlock()
		switch {
		case r.URL.Path == "/heal":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/jobs" && how == "refuse":
			fail(w, http.StatusInternalServerError, "the scheduler is busy")
		case r.URL.Path == "/jobs" && how == "proxy":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html><body><h1>502 Bad Gateway</h1></body></html>"))
		case r.URL.Path == "/jobs" && how == "drop":
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				conn.Close()
			}
		default:
			held.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s := newSession(t, a)

	for _, screen := range []struct {
		name, hash, fail string
		want             []string
	}{
		{"a gone conversation", "#session/GONE", "", []string{
			"busy=true,true ;", "This conversation no longer exists:Back to conversations busy=false |"}},
		{"a refused screen", "#jobs", "refuse", []string{
			"busy=true,true ;", "This screen did not load:Try again busy=false ;", "says=", "the scheduler is busy",
			"No jobs scheduled: busy=false |"}},
		{"an unreachable agent", "#jobs", "drop", []string{
			"busy=true,true ;", "The agent cannot be reached:Try again busy=false ;", "No jobs scheduled: busy=false |"}},
		{"an agent behind its proxy, restarting", "#jobs", "proxy", []string{
			"busy=true,true ;", "The agent is not answering:Try again busy=false ;", "No jobs scheduled: busy=false |"}},
	} {
		mu.Lock()
		mode = screen.fail
		mu.Unlock()
		args, _ := json.Marshal(map[string]string{"url": srv.URL + "/" + screen.hash})
		seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
		if !seen.OK {
			t.Fatalf("%s: web_browse: %s", screen.name, seen.Error)
		}
		m := regexp.MustCompile(`states: ([^\n]*)`).FindStringSubmatch(seen.Content)
		if m == nil {
			t.Fatalf("%s: the probe wrote nothing; the browser read:\n%s", screen.name, seen.Content)
		}
		for _, want := range screen.want {
			if !strings.Contains(m[1], want) {
				t.Errorf("%s: want %q in %s", screen.name, want, m[1])
			}
		}
	}
}
