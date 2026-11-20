package app

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// formsProbe waits until the screen shows the text its address names as
// ready, then writes how it is laid out: the headings of its groups, how many
// chips it shows, the labels of the first group's rows, what the tools row
// says and whether it opens. It then taps the tools row, writes how many
// ticks the sheet that opens holds, unticks the first, and writes what the
// row says then.
const formsProbe = `(function probe(left) {
  const view = document.getElementById('view');
  const ready = new URLSearchParams(location.search).get('ready') || '';
  const tools = view.querySelector('.row-tools');
  if (left && (!tools || !view.textContent.includes(ready))) { fetch('/held').then(() => probe(left - 1)); return; }
  const shown = n => n.getClientRects().length > 0;
  const said = () => view.querySelector('.row-tools .v').textContent;
  const first = view.querySelector('.group .rows');
  const seen = ['groups=' + [...view.querySelectorAll('.group > h2')].map(h => h.textContent).join('/'),
    'chips=' + [...view.querySelectorAll('.pill')].filter(shown).length,
    'rows=' + [...first.children].map(r => (r.querySelector('.l') || r).textContent).join('/'),
    'tools=' + said(), 'opens=' + !!tools.querySelector('.chev')];
  tools.click();
  const picks = [...document.querySelectorAll('.dialog-back .pick')];
  seen.push('sheet=' + picks.length);
  if (picks.length) { picks[0].click(); seen.push('ticked=' + said()); }
  const close = document.querySelector('.dialog-close');
  if (close) close.click();
  const out = document.createElement('pre');
  out.textContent = 'forms: ' + seen.join(' ; ') + ' |';
  document.body.prepend(out);
})(200);`

// Starting a conversation and its details are grouped one-line rows, the
// tools summed up on one row that opens a sheet of ticks where they can still
// be chosen, and named in full where they cannot (webui.html, "Per-session
// details"). In a real browser against the interface as shipped.
func TestNewAndDetailsAreGroupedRowsWithChoicesOneLevelDown(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "forms:", formsProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s := newSession(t, a)

	first := "rows=Title/Model/Tools/Skills/Persona ;"
	for _, screen := range []struct {
		name, hash, ready string
		want              []string
	}{
		{"a new conversation", "#new", "Start conversation", []string{
			"groups=This conversation/Variables/Compaction ;", "chips=0 ;", first,
			"tools=All ", "opens=true", "ticked=All but "}},
		{"its details", "#settings/" + s.ID, "Delete conversation", []string{
			"groups=This conversation/Compaction/Variables/Export or delete ;", "chips=0 ;", first,
			"web_browse", "opens=false", "sheet=0 "}},
	} {
		page := srv.URL + "/?" + url.Values{"ready": {screen.ready}}.Encode() + screen.hash
		args, _ := json.Marshal(map[string]string{"url": page})
		seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
		if !seen.OK {
			t.Fatalf("%s: web_browse: %s", screen.name, seen.Error)
		}
		m := regexp.MustCompile(`forms: ([^\n]*)`).FindStringSubmatch(seen.Content)
		if m == nil {
			t.Errorf("%s: the probe wrote nothing; the browser read:\n%s", screen.name, seen.Content)
			continue
		}
		got := m[1]
		for _, want := range screen.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: want %q in %s", screen.name, want, got)
			}
		}
		if screen.hash == "#new" {
			n := regexp.MustCompile(`sheet=(\d+)`).FindStringSubmatch(got)
			if n == nil {
				t.Errorf("%s: the tools row opened no sheet: %s", screen.name, got)
			} else if k, _ := strconv.Atoi(n[1]); k < 2 {
				t.Errorf("%s: the sheet ticks %s tools, not every tool the agent has: %s", screen.name, n[1], got)
			}
		}
	}
}
