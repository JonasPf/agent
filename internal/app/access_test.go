package app

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// browseFor opens the interface at hash in a real browser and returns what
// the probe wrote after its marker.
func browseFor(t *testing.T, a *App, srv *httptest.Server, sessionID, hash, marker string) string {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/" + hash})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: sessionID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse %s: %s", hash, seen.Error)
	}
	m := regexp.MustCompile(regexp.QuoteMeta(marker) + ` ([^\n]*)`).FindStringSubmatch(seen.Content)
	if m == nil {
		t.Fatalf("%s: the probe wrote nothing; the browser read:\n%s", hash, seen.Content)
	}
	return m[1]
}

// headingsProbe waits for the screen to be drawn, and for a conversation's
// answer where there is one, then writes every heading a screen reader would
// list, in order, as level and name.
const headingsProbe = `(function probe(left) {
  const view = document.getElementById('view');
  const drawn = view.children.length && view.getAttribute('aria-busy') !== 'true';
  const talk = /^#session\//.test(location.hash);
  if (left && (!drawn || (talk && !document.querySelector('.msg.agent')))) { fetch('/held').then(() => probe(left - 1)); return; }
  const heard = n => !n.closest('[hidden],[aria-hidden="true"],[inert]') && getComputedStyle(n).display !== 'none';
  const name = n => (n.getAttribute('aria-label') || n.textContent).replace(/\s+/g, ' ').trim();
  const level = n => n.getAttribute('aria-level') || n.tagName.slice(1);
  const heads = [...document.querySelectorAll('h1,h2,h3,h4,h5,h6,[role=heading]')].filter(heard)
    .map(n => 'h' + level(n) + ':' + name(n) + (n.closest('header') ? '@header' : ''));
  const out = document.createElement('pre');
  out.textContent = 'heads: ' + heads.join(' | ') + ' |';
  document.body.prepend(out);
})(200);`

// Each screen's title is its one top-level heading, and in a conversation each
// message is a heading below it naming who spoke and when, so a screen
// reader's headings move from message to message (webui.html, "Keyboard and
// screen reader"). In a real browser against the interface as shipped.
func TestEachMessageIsAHeadingUnderTheScreensTitle(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "heads:", headingsProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "The beds are dry."}).URL
	s := newSession(t, a)
	s.Title = "Greenhouse sensors"
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}
	if err := a.SendUserMessage(s.ID, "How are the beds?"); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)

	got := browseFor(t, a, srv, s.ID, "#session/"+s.ID, "heads:")
	if !strings.HasPrefix(got, "h1:Greenhouse sensors@header | ") {
		t.Errorf("the conversation's title is not its first heading, in the header: %s", got)
	}
	if n := strings.Count(got, "h1:"); n != 1 {
		t.Errorf("the conversation has %d top-level headings, want one: %s", n, got)
	}
	said := regexp.MustCompile(`h2:(You|Agent), [^|]*\d:\d\d`).FindAllStringSubmatch(got, -1)
	var who []string
	for _, m := range said {
		who = append(who, m[1])
	}
	if strings.Join(who, ",") != "You,Agent" {
		t.Errorf("want a heading naming who spoke and when for each message, You then Agent; got %v in %s", who, got)
	}

	got = browseFor(t, a, srv, s.ID, "#jobs", "heads:")
	if !strings.HasPrefix(got, "h1:Jobs@header") || strings.Count(got, "h1:") != 1 {
		t.Errorf("the jobs screen's title is not its one top-level heading: %s", got)
	}
}

// fieldsProbe waits for the screen to be drawn and writes each field with
// no name a screen reader can say, other than its placeholder; then each
// row's delete, by the name it is heard by and whether it follows its row.
const fieldsProbe = `(function probe(left) {
  const view = document.getElementById('view');
  const drawn = view.children.length && view.getAttribute('aria-busy') !== 'true';
  if (left && !drawn) { fetch('/held').then(() => probe(left - 1)); return; }
  const said = n => {
    const by = n.getAttribute('aria-labelledby');
    if (by) return by.split(/\s+/).map(id => (document.getElementById(id) || {}).textContent || '').join(' ').trim();
    return (n.getAttribute('aria-label') || [...(n.labels || [])].map(l => l.textContent).join(' ') || n.title || '').trim();
  };
  const fields = [...document.querySelectorAll('input,textarea,select')]
    .filter(n => n.type !== 'hidden' && n.type !== 'file' && !n.closest('[hidden]'));
  const unnamed = fields.filter(n => !said(n)).map(n => n.tagName.toLowerCase() + '(' + (n.placeholder || n.className) + ')');
  const dels = [...document.querySelectorAll('.row-del')].map(d => said(d) || d.textContent +
    (d.previousElementSibling && d.previousElementSibling.classList.contains('row-item') ? '' : ' before its row'));
  const out = document.createElement('pre');
  out.textContent = 'fields: ' + fields.length + ' unnamed=[' + unnamed.join(', ') + '] deletes=[' + dels.join(', ') + '] |';
  document.body.prepend(out);
})(200);`

// Every field says what it is for to a screen reader, and a row's delete
// names the row it deletes and comes after it (webui.html, "Keyboard and
// screen reader"). In a real browser against the interface as shipped, on
// each screen with a field or a delete.
func TestEveryFieldIsNamedAndADeleteNamesItsRow(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "fields:", fieldsProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s := newSession(t, a)
	s.Title = "Greenhouse sensors"
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}

	for _, hash := range []string{"#session/" + s.ID, "#new", "#fork/" + s.ID, "#settings/" + s.ID, "#search", "#sessions", "#tools"} {
		got := browseFor(t, a, srv, s.ID, hash, "fields:")
		if !strings.Contains(got, "unnamed=[]") {
			t.Errorf("%s: a field is named only by its placeholder or not at all: %s", hash, got)
		}
		if strings.HasPrefix(got, "fields: 0 ") && hash != "#sessions" && hash != "#tools" {
			t.Errorf("%s: the probe found no fields, so it checked nothing: %s", hash, got)
		}
		if hash == "#sessions" && !strings.Contains(got, "deletes=[Delete Greenhouse sensors]") {
			t.Errorf("a conversation's delete does not name it, or comes before it: %s", got)
		}
	}
}

// drawerProbe, on a screen as narrow as a phone's, writes whether the closed
// drawer's first button can take the focus and whether a screen reader would
// read the drawer; then opens it from the menu button and writes where the
// focus went and whether the screen behind can take it; then presses Escape
// and writes whether the drawer closed and where the focus went back to.
const drawerProbe = `(function probe(left) {
  const nav = document.getElementById('side-nav');
  if (left && !(nav && nav.children.length && document.getElementById('view').children.length)) { fetch('/held').then(() => probe(left - 1)); return; }
  const side = document.getElementById('side');
  const hidden = () => getComputedStyle(side).visibility === 'hidden' || side.inert || side.getAttribute('aria-hidden') === 'true';
  const takes = n => { n.focus(); return document.activeElement === n; };
  const seen = ['width=' + innerWidth];
  seen.push('closed: focusable=' + takes(document.getElementById('side-new')) + ' read=' + !hidden());
  document.getElementById('menu').click();
  const inside = side.contains(document.activeElement);
  const behind = takes(document.querySelector('#view button'));
  seen.push('open: focus-inside=' + inside + ' behind-focusable=' + behind + ' read=' + !hidden());
  side.querySelector('button').focus();
  document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  seen.push('escape: open=' + document.body.classList.contains('drawer') + ' focus=' + (document.activeElement.id || document.activeElement.tagName));
  const out = document.createElement('pre');
  out.textContent = 'drawer: ' + seen.join(' ; ') + ' |';
  document.body.prepend(out);
})(200);`

// On a phone the closed drawer can be neither reached with the keyboard nor
// read; opened, it takes the focus and the screen behind is out of reach,
// and Escape closes it and puts the focus back on the button that opened it
// (webui.html, "Keyboard and screen reader"). In a real browser against the
// interface as shipped, as narrow as the drawer is used.
func TestTheClosedDrawerIsOutOfReachOnAPhone(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "drawer:", drawerProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s := newSession(t, a)

	got := browseFor(t, a, srv, s.ID, "#sessions", "drawer:")
	w := regexp.MustCompile(`width=(\d+)`).FindStringSubmatch(got)
	if w == nil || len(w[1]) > 3 || w[1] >= "900" {
		t.Fatalf("the browser is not as narrow as a phone, so there is no drawer to test: %s", got)
	}
	for _, want := range []string{
		"closed: focusable=false read=false",
		"open: focus-inside=true behind-focusable=false read=true",
		"escape: open=false focus=menu",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in %s", want, got)
		}
	}
}
