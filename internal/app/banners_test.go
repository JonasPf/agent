package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// breakerProbe waits for the banner a paused scheduler puts across the top,
// writes whether it shows and what its one line says, opens it, and writes
// what it then says.
const breakerProbe = `(function probe(left) {
  const b = document.getElementById('breaker');
  const head = b.querySelector('.why');
  if (left && (b.hidden || !head)) { fetch('/held').then(() => probe(left - 1)); return; }
  const line = s => s.replace(/\s+/g, ' ').trim();
  const seen = ['shown=' + !b.hidden, 'line=' + line(head.textContent)];
  head.click();
  const more = b.querySelector('.more');
  seen.push('open=' + !more.hidden + ' more=' + line(more.textContent));
  const out = document.createElement('pre');
  out.textContent = 'breaker: ' + seen.join(' ; ') + ' |';
  document.body.prepend(out);
})(200);`

// A paused scheduler is one banner across every screen naming its cause in
// words, and opens to the rest (jobs.html, "Circuit breaker"; webui.html,
// "Status and cost"). In a real browser against the interface as shipped,
// with the breaker tripped as it is in use: a wake the gateway refuses for
// want of credit.
func TestAPausedSchedulerIsOneBannerNamingItsCause(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "breaker:", breakerProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	broke := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"Insufficient credits","code":402}}`, http.StatusPaymentRequired)
	}))
	t.Cleanup(broke.Close)
	a.or = NewOpenRouter("test-key")
	a.or.base = broke.URL

	s, browser := newSession(t, a), newSession(t, a)
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Has anything changed?", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}
	a.sched.tick(t.Context())
	deadline := time.Now().Add(5 * time.Second)
	for !a.sched.State().Open && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !a.sched.State().Open {
		t.Fatal("a wake the gateway refused for want of credit did not pause the scheduler")
	}

	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/#sessions"})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: browser.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	m := regexp.MustCompile(`breaker: ([^\n]*)`).FindStringSubmatch(seen.Content)
	if m == nil {
		t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
	}
	got := m[1]
	for _, want := range []string{"shown=true", "line=Jobs are paused", "Insufficient credits", "open=true", "every five minutes"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in %s", want, got)
		}
	}
	if strings.Contains(got, `{"error"`) {
		t.Errorf("the gateway's JSON is shown as the cause: %s", got)
	}
}

// connProbe waits for the page's socket to open and writes whether the line
// saying the connection is down shows. It then has the agent refuse the page,
// closes the socket, and after longer than a phone takes to wake writes the
// line again; then has the agent answer again and waits for the line to go.
// The browser's clock is virtual and stands still while a request is pending,
// as one always is while the page is held open; so the probe writes what it
// has at once, which releases the hold, and waits on timers from there. A
// socket opening does not hold the clock, so each step of the last wait also
// makes a request, which lets real time pass for it.
const connProbe = `(function probe(left) {
  if (left && (!ws || ws.readyState !== 1)) { fetch('/held').then(() => probe(left - 1)); return; }
  const conn = document.getElementById('conn');
  const seen = ['open: hidden=' + conn.hidden];
  const out = document.createElement('pre');
  const write = () => { out.textContent = 'conn: ' + seen.join(' ; ') + ' |'; };
  write();
  document.body.prepend(out);
  fetch('/cut').then(() => {
    ws.close();
    setTimeout(() => {
      seen.push('cut: hidden=' + conn.hidden + ' says=' + conn.textContent.replace(/\s+/g, ' ').trim());
      fetch('/mend').then(() => (function wait(n) {
        if (n && !conn.hidden) { setTimeout(() => fetch('/held').then(() => wait(n - 1)), 100); return; }
        seen.push('mended: hidden=' + conn.hidden);
        write();
      })(80));
    }, LINK_GRACE + 1000);
  });
})(200);`

// A connection to the agent down for longer than a moment says so across the
// top of every screen, and the line goes once it is back (webui.html,
// "States"). In a real browser against the interface as shipped, with the
// agent refusing the page's socket and status until the page asks it not to.
func TestALostConnectionSaysSoUntilItIsBack(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	var cut atomic.Bool
	held := heldUntil(t, a.routes(), "conn:", connProbe)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/cut":
			cut.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/mend":
			cut.Store(false)
			w.WriteHeader(http.StatusNoContent)
		case cut.Load() && (r.URL.Path == "/status" || r.URL.Path == "/ws"):
			http.Error(w, "down", http.StatusServiceUnavailable)
		default:
			held.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s := newSession(t, a)

	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/#sessions"})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	m := regexp.MustCompile(`conn: ([^\n]*)`).FindStringSubmatch(seen.Content)
	if m == nil {
		t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
	}
	got := m[1]
	for _, want := range []string{"open: hidden=true ;", "cut: hidden=false says=Not connected to the agent", "mended: hidden=true |"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in %s", want, got)
		}
	}
}
