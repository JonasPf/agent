package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// onceProbe waits until the screen has drawn its list, in the pane on the
// list screen or in the rail beside a conversation, then writes how many
// conversations the pane and the rail each list, and what the rail links.
const onceProbe = `(function probe(left) {
  const count = sel => document.querySelectorAll(sel).length;
  if (left && !count('#view .row-item') && !count('#side-list .side-item')) { fetch('/held').then(() => probe(left - 1)); return; }
  const out = document.createElement('pre');
  out.textContent = 'once: pane=' + count('#view .row-item') + ' rail=' + count('#side-list .side-item') + ' nav=' +
    [...document.querySelectorAll('#side-nav button')].map(b => b.textContent + (b.getAttribute('aria-current') ? '*' : '')).join('/') + ' |';
  document.body.prepend(out);
})(200);`

// The conversation list is drawn once: in the pane on the list screen, and in
// the rail beside any other screen (webui.html, "Shell"). In a real browser
// against the interface as shipped.
func TestTheConversationListIsDrawnOnce(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "once:", onceProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s, other := newSession(t, a), newSession(t, a)

	for _, screen := range []struct{ name, hash, want string }{
		{"the list", "#sessions", "once: pane=2 rail=0 nav=Conversations*/Jobs/Plugins/Skills/More |"},
		{"a conversation", "#session/" + other.ID, "once: pane=0 rail=2 nav=Conversations/Jobs/Plugins/Skills/More |"},
	} {
		args, _ := json.Marshal(map[string]string{"url": srv.URL + "/" + screen.hash})
		seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
		if !seen.OK {
			t.Fatalf("%s: web_browse: %s", screen.name, seen.Error)
		}
		m := regexp.MustCompile(`once: [^\n]*`).FindString(seen.Content)
		if m == "" {
			t.Fatalf("%s: the probe wrote nothing; the browser read:\n%s", screen.name, seen.Content)
		}
		if m != screen.want {
			t.Errorf("%s: got %q, want %q", screen.name, m, screen.want)
		}
	}
}

// moreProbe waits for the More panel to name what changed, writes the
// headings of its groups and the labels of their rows, then opens what
// changed and writes whether a sheet holds it and what it says.
const moreProbe = `(function probe(left) {
  const view = document.getElementById('view');
  const row = [...view.querySelectorAll('.item')].find(r => r.querySelector('.l').textContent === 'What changed');
  if (left && !row) { fetch('/held').then(() => probe(left - 1)); return; }
  const seen = [];
  for (const g of view.querySelectorAll('.group')) {
    const h = g.querySelector(':scope > h2');
    seen.push((h ? h.textContent : '-') + '=' + [...g.querySelectorAll('.item .l')].map(l => l.textContent).join('/'));
  }
  seen.push('pills=' + view.querySelectorAll('.tag').length);
  row.click();
  const sheet = document.querySelector('.dialog-back .changelog');
  seen.push('sheet=' + !!sheet + ' says=' + (sheet ? sheet.textContent.replace(/\s+/g, ' ').trim() : ''));
  const out = document.createElement('pre');
  out.textContent = 'more: ' + seen.join(' ; ') + ' |';
  document.body.prepend(out);
})(200);`

// More is a few groups of one-line rows, leading to what the rail does not and
// holding this device's settings and the build, and what changed opens in a
// sheet (webui.html, "Panels", "Version and changelog"). In a real browser
// against the interface as shipped.
func TestMoreIsGroupedRowsAndOpensWhatChangedInASheet(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.cfg.ChangelogPath = filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(a.cfg.ChangelogPath, []byte("# Changelog\n\n## 2026-11-20\n\n- More is grouped rows.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "more:", moreProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s := newSession(t, a)

	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/#panels"})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	m := regexp.MustCompile(`more: ([^\n]*)`).FindStringSubmatch(seen.Content)
	if m == nil {
		t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
	}
	got := m[1]
	for _, want := range []string{
		"-=Personas/Metrics ;",
		"This device=Notifications/Accent ;",
		"This agent=Version/What changed ;",
		"pills=0 ;",
		"sheet=true says=",
		"More is grouped rows.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in %s", want, got)
		}
	}
}
