package app

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// listProbe waits for the conversation list, writes how many lines of detail
// each row has, then swipes the first row to the left as a finger would,
// clicks where the swipe ended, and writes whether the row slid open and the
// page stayed on the list.
const listProbe = `(function probe(left) {
  const wrap = document.querySelector('.row-wrap');
  if (left && !wrap) { fetch('/held').then(() => probe(left - 1)); return; }
  const lines = [...document.querySelectorAll('.row-wrap')].map(w => w.querySelectorAll('.row-item .s').length);
  const row = wrap.querySelector('.row-item');
  const r = row.getBoundingClientRect();
  const y = r.top + r.height / 2;
  const touch = (type, x) => row.dispatchEvent(new PointerEvent(type, { bubbles: true, clientX: x, clientY: y,
    pointerId: 1, pointerType: 'touch', isPrimary: true }));
  touch('pointerdown', r.right - 20);
  touch('pointermove', r.right - 60);
  touch('pointermove', r.right - 140);
  touch('pointerup', r.right - 140);
  const before = location.hash;
  row.click();
  const out = document.createElement('pre');
  out.textContent = 'list: lines=' + lines.join(',') + ' open=' + wrap.classList.contains('open') +
    ' moved=' + (row.style.transform || 'none') + ' stayed=' + (location.hash === before) + ' |';
  document.body.prepend(out);
})(200);`

// A row of the conversation list is its title and one line of detail, and a
// swipe to the left uncovers its delete without opening the conversation
// (webui.html, "Session management"). In a real browser against the
// interface as shipped.
func TestAListRowIsOneLineAndASwipeUncoversItsDelete(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "list:", listProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)

	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	var s *Session
	for range 2 {
		s = newSession(t, a)
		if err := a.SendUserMessage(s.ID, "note this"); err != nil {
			t.Fatal(err)
		}
		settle(t, a, s.ID)
	}

	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/"})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	m := regexp.MustCompile(`list: ([^\n]*)`).FindStringSubmatch(seen.Content)
	if m == nil {
		t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
	}
	got := m[1]
	for _, want := range []string{"lines=1,1 ", "open=true", "moved=translateX(-96px)", "stayed=true"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in %s", want, got)
		}
	}
}
