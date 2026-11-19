package app

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// fontProbe waits until the screen shows the text the page's address names
// as ready, then writes at the top of the page every size its visible text is
// drawn at, with a few of the elements drawn at each, as the browser computed
// them. Inline styles, a browser's own defaults and relative sizes all show
// here, which no reading of the stylesheet can promise.
const fontProbe = `(function probe(left) {
  const ready = new URLSearchParams(location.search).get('ready') || '';
  const shown = document.body.textContent.includes(ready);
  if (left && !shown) { fetch('/held').then(() => probe(left - 1)); return; }
  const sizes = {};
  for (const e of document.querySelectorAll('body *')) {
    if (![...e.childNodes].some(n => n.nodeType === 3 && n.textContent.trim())) continue;
    if (!e.getClientRects().length || e.closest('script, style, template')) continue;
    const cs = getComputedStyle(e);
    if (cs.visibility === 'hidden') continue;
    const who = e.tagName.toLowerCase() + ((e.getAttribute('class') || '').trim() ? '.' + e.getAttribute('class').trim().split(/\s+/).join('.') : '');
    (sizes[parseFloat(cs.fontSize)] ||= new Set()).add(who);
  }
  const out = document.createElement('pre');
  out.textContent = (shown ? '' : 'not ready: ') + 'font sizes: ' + Object.keys(sizes).sort((a, b) => a - b)
    .map(px => px + ' ' + [...sizes[px]].slice(0, 6).join(',')).join('; ');
  document.body.prepend(out);
})(200);`

// Every piece of text on the main screens is drawn at one of the five sizes
// of the scale (webui.html, "Look"), as a real browser computes it from the
// interface as shipped: the list, a conversation with a tool call and a reply
// that has a heading, a list and code in it, its details, and a new
// conversation.
func TestEveryTextOnTheMainScreensIsOneOfTheFiveSizes(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "font sizes:", fontProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)

	model := scriptedModel(t, toolStep{tool: "session_search", args: `{"query":"lisbon"}`},
		toolStep{text: "Here is `notes.md`:\n\n## Next\n\n- one\n- two\n\n```\n# Plan\n```"})
	a.or = NewOpenRouter("test-key")
	a.or.base = model.URL
	s := newSession(t, a)
	if err := a.SendUserMessage(s.ID, "find lisbon"); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)

	scale := map[string]bool{"12": true, "14": true, "16": true, "18": true, "24": true}
	found := regexp.MustCompile(`(not ready: )?font sizes: ([^\n]*)`)
	for _, screen := range []struct{ name, hash, ready string }{
		{"the list", "", "A title"},
		{"a conversation", "#session/" + s.ID, "Here is"},
		{"its details", "#settings/" + s.ID, "fixed for its life"},
		{"a new conversation", "#new", "Start conversation"},
	} {
		page := srv.URL + "/?" + url.Values{"ready": {screen.ready}}.Encode() + screen.hash
		args, _ := json.Marshal(map[string]string{"url": page})
		seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
		if !seen.OK {
			t.Fatalf("%s: web_browse: %s", screen.name, seen.Error)
		}
		m := found.FindStringSubmatch(seen.Content)
		if m == nil {
			t.Errorf("%s: the probe wrote nothing; the browser read:\n%s", screen.name, seen.Content)
			continue
		}
		if m[1] != "" {
			t.Errorf("%s never showed %q", screen.name, screen.ready)
		}
		for _, part := range strings.Split(m[2], "; ") {
			px, who, _ := strings.Cut(part, " ")
			if !scale[px] {
				t.Errorf("%s draws text at %spx, outside the scale: %s", screen.name, px, who)
			}
		}
	}
}

// serifProbe waits for the conversation's reply, lets the browser lay it out
// and fetch the faces it needs, then writes how the reply and the operator's
// message are set, which faces loaded, and what the line above each message
// shows once what only a screen reader hears, and its buttons, are set aside.
const serifProbe = `(function probe(left) {
  const reply = document.querySelector('.msg.agent .bub');
  if (left && !reply) { fetch('/held').then(() => probe(left - 1)); return; }
  if (reply) reply.offsetHeight;
  document.fonts.ready.then(() => {
    const shown = n => {
      if (!n) return '-';
      const c = n.cloneNode(true);
      c.querySelectorAll('.sr, button').forEach(x => x.remove());
      return c.textContent.trim();
    };
    const cs = reply ? getComputedStyle(reply) : null;
    const you = document.querySelector('.msg.you .bub');
    const out = document.createElement('pre');
    out.textContent = 'serif: family=' + (cs ? cs.fontFamily : '-') + ' | size=' + (cs ? cs.fontSize : '-') +
      ' | you=' + (you ? getComputedStyle(you).fontFamily : '-') +
      ' | loaded=' + [...document.fonts].filter(f => f.status === 'loaded').map(f => f.family.replace(/"/g, '') + ' ' + f.style + ' ' + f.weight).join(',') +
      ' | agentlabel=' + shown(document.querySelector('.msg.agent .who')) +
      ' | youlabel=' + shown(document.querySelector('.msg.you .who')) + ' |';
    document.body.prepend(out);
  });
})(200);`

// The agent's replies are set in the serif shipped with the interface, so
// every device draws them alike; what the operator typed stays in the sans
// that everything acted on is set in; and the line above a message carries
// its time, not a speaker the shape of the message already names
// (webui.html, "Look").
func TestRepliesAreSetInTheSerifShippedWithTheInterface(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "serif:", serifProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)

	model := scriptedModel(t, toolStep{text: "The notes are kept, and **Plan** heads them."})
	a.or = NewOpenRouter("test-key")
	a.or.base = model.URL
	s := newSession(t, a)
	if err := a.SendUserMessage(s.ID, "keep my notes"); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)

	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/#session/" + s.ID})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	m := regexp.MustCompile(`serif: ([^\n]*)`).FindStringSubmatch(seen.Content)
	if m == nil {
		t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
	}
	got := m[1]
	field := func(name string) string {
		f := regexp.MustCompile(name + `=([^|]*)\|`).FindStringSubmatch(got)
		if f == nil {
			t.Fatalf("no %s in %s", name, got)
		}
		return strings.TrimSpace(f[1])
	}
	if f := field("family"); !strings.HasPrefix(f, `"Agent Serif"`) {
		t.Errorf("a reply is set in %s, want the shipped Agent Serif first", f)
	}
	if f := field("size"); f != "18px" {
		t.Errorf("a reply is set at %s, want 18px", f)
	}
	if f := field("you"); strings.Contains(f, "Agent Serif") {
		t.Errorf("what the operator typed is set in the serif: %s", f)
	}
	if f := field("loaded"); !strings.Contains(f, "Agent Serif normal 400") {
		t.Errorf("the shipped serif never loaded; loaded faces: %s", f)
	}
	if f := field("agentlabel"); strings.Contains(strings.ToLower(f), "agent") {
		t.Errorf("the line above a reply still names the agent: %q", f)
	}
	if f := field("youlabel"); strings.Contains(strings.ToLower(f), "you") {
		t.Errorf("the line above the operator's message still names them: %q", f)
	}
}

// The serif is served as what it is, so a browser that checks takes it.
func TestTheShippedSerifIsServedAsAFont(t *testing.T) {
	a := newTestApp(t)
	a.cfg.WebDir = "../../web"
	for _, style := range []string{"regular", "italic", "bold"} {
		w := call(t, a, "GET", "/fonts/agent-serif-"+style+".woff2", "")
		if w.Code != 200 {
			t.Errorf("%s: status %d", style, w.Code)
			continue
		}
		if ct := w.Header().Get("Content-Type"); ct != "font/woff2" {
			t.Errorf("%s is served as %q, want font/woff2", style, ct)
		}
	}
}
