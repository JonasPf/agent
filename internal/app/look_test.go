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
