package app

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// slideProbe waits for the list, opens the conversation the page's address
// names, and on each move — that one, then the browser's back — writes what
// the browser is moving: whether the screen left behind is on a layer of its
// own, the transforms the pane's main and that layer pass through, and whether
// the header offers the drawer and the way back at once.
const slideProbe = `(function probe(left) {
  const q = new URLSearchParams(location.search);
  if (left && !document.body.textContent.includes(q.get('ready'))) { fetch('/held').then(() => probe(left - 1)); return; }
  const seen = [];
  const shown = id => { const n = document.getElementById(id); return !!n && getComputedStyle(n).display !== 'none'; };
  const moves = n => n ? n.getAnimations().map(a => a.effect.getKeyframes().map(k => k.transform).join('>')).join('|') : '';
  const wait = (n, then) => n ? fetch('/held').then(() => wait(n - 1, then)) : then();
  window.addEventListener('hashchange', () => {
    const layer = document.querySelector('.leaving');
    seen.push((seen.length ? 'back' : 'deeper') + ' layer=' + !!layer + ' main=' + (moves(document.getElementById('main')) || '-') +
      ' leaving=' + (moves(layer) || '-') + ' both=' + (shown('menu') && shown('back')));
    if (seen.length === 1) { wait(16, () => history.back()); return; }
    const out = document.createElement('pre');
    out.textContent = 'slides: ' + seen.join('; ');
    document.body.prepend(out);
  });
  location.hash = '#session/' + q.get('open');
})(200);`

// Going deeper slides the new screen in from the right over the one it came
// from, and the browser's back slides it away to the right again, in a real
// browser against the interface as shipped (webui.html, "Shell").
func TestGoingDeeperSlidesInAndGoingBackSlidesAway(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "slides:", slideProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)

	model := scriptedModel(t, toolStep{text: "Noted."})
	a.or = NewOpenRouter("test-key")
	a.or.base = model.URL
	s := newSession(t, a)
	if err := a.SendUserMessage(s.ID, "note this"); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)

	page := srv.URL + "/?" + url.Values{"ready": {"A title"}, "open": {s.ID}}.Encode()
	args, _ := json.Marshal(map[string]string{"url": page})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	got := regexp.MustCompile(`slides: ([^\n]*)`).FindStringSubmatch(seen.Content)
	if got == nil {
		t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
	}
	moves := strings.Split(got[1], "; ")
	if len(moves) != 2 {
		t.Fatalf("saw %d moves, want the way in and the way back: %s", len(moves), got[1])
	}
	deeper, back := moves[0], moves[1]
	for _, want := range []string{"layer=true", "main=translateX(100%)>", "leaving=none>translateX(-", "both=false"} {
		if !strings.Contains(deeper, want) {
			t.Errorf("opening the conversation: want %q in %s", want, deeper)
		}
	}
	for _, want := range []string{"layer=true", "main=translateX(-", "leaving=none>translateX(100%)", "both=false"} {
		if !strings.Contains(back, want) {
			t.Errorf("going back: want %q in %s", want, back)
		}
	}
}
