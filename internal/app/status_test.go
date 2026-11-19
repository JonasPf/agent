package app

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// quietProbe waits for the conversation and its status line, writes how its
// opening reads — the prompt folded or not, the status line's height and
// whether its details show, where the answer's cache hit sits — then taps
// the status line's toggle and writes whether the details opened.
const quietProbe = `(function probe(left) {
  const now = document.querySelector('.status-now');
  if (left && (!now || !document.querySelector('.msg.agent'))) { fetch('/held').then(() => probe(left - 1)); return; }
  const secs = document.querySelector('.prompt .secs');
  const more = document.querySelector('.status-more');
  const shown = n => !!n && !n.hidden && n.getClientRects().length > 0;
  const seen = ['prompt=' + (secs ? (shown(secs) ? 'open' : 'folded') : 'none'),
    'line=' + (now ? now.offsetHeight : 0) + 'px', 'details=' + (shown(more) ? 'shown' : 'hidden'),
    'hit=' + (document.querySelector('.msg.agent .who .cachehit') ? 'with-time' : 'elsewhere')];
  const toggle = document.querySelector('.status-toggle');
  if (toggle) toggle.click();
  seen.push('tapped=' + (shown(document.querySelector('.status-more')) ? 'shown' : 'hidden'));
  const out = document.createElement('pre');
  out.textContent = 'quiet: ' + seen.join(' ') + ' |';
  document.body.prepend(out);
})(200);`

// A conversation opens quietly (webui.html, "Conversation view"): the prompt
// folded into one line, the status line one line with its details a tap
// away, and an answer's cache hit beside its time. In a real browser against
// the interface as shipped.
func TestAConversationOpensWithThePromptFoldedAndOneStatusLine(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "quiet:", quietProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)

	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s := newSession(t, a)
	if err := a.SendUserMessage(s.ID, "note this"); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)

	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/#session/" + s.ID})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	m := regexp.MustCompile(`quiet: ([^\n]*)`).FindStringSubmatch(seen.Content)
	if m == nil {
		t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
	}
	got := m[1]
	for _, want := range []string{"prompt=folded", "details=hidden", "hit=with-time", "tapped=shown"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in %s", want, got)
		}
	}
	// One row of 12px text, its controls a touch target tall; two rows are more.
	h := regexp.MustCompile(`line=(\d+)px`).FindStringSubmatch(got)
	if h == nil {
		t.Fatalf("no height for the status line in %s", got)
	}
	if px, _ := strconv.Atoi(h[1]); px == 0 || px > 40 {
		t.Errorf("the status line is not one line: %s", got)
	}
}
