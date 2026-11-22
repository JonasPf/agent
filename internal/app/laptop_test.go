package app

import (
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// capsProbe waits for the screen to be drawn and writes every element whose
// own text the browser sets in capitals, as it computed them from the
// stylesheet: hidden ones too, since a fold opened later shows them.
const capsProbe = `(function probe(left) {
  const view = document.getElementById('view');
  const drawn = view.children.length && view.getAttribute('aria-busy') !== 'true';
  if (left && !drawn) { fetch('/held').then(() => probe(left - 1)); return; }
  const caps = [];
  for (const e of document.querySelectorAll('body *')) {
    if (![...e.childNodes].some(n => n.nodeType === 3 && n.textContent.trim())) continue;
    if (getComputedStyle(e).textTransform !== 'uppercase') continue;
    caps.push(e.tagName.toLowerCase() + '.' + (e.getAttribute('class') || '').trim().replace(/\s+/g, '.') + '"' + e.textContent.trim().slice(0, 20) + '"');
  }
  const out = document.createElement('pre');
  out.textContent = 'caps: [' + caps.join(', ') + '] |';
  document.body.prepend(out);
})(200);`

// A label reads as it is written: nothing on a screen is set in capitals by
// the stylesheet (webui.html, "Look"). In a real browser against the
// interface as shipped, on the screens that held such labels: a job's wake,
// a tool's definition on Plugins, a skill's front matter, and the forms.
func TestNoLabelIsSetInCapitals(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "caps:", capsProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "The beds are dry."}).URL
	s := newSession(t, a)
	if _, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m", Prompt: "Check the beds.", AfterActing: afterContinue}); err != nil {
		t.Fatal(err)
	}

	for _, hash := range []string{"#sessions", "#session/" + s.ID, "#new", "#settings/" + s.ID, "#jobs", "#tools", "#skills", "#metrics"} {
		got := browseFor(t, a, srv, s.ID, hash, "caps:")
		if got != "[] |" {
			t.Errorf("%s sets text in capitals: %s", hash, got)
		}
	}
}

// laptopProbe, in the page on top, opens the interface in a frame as wide as
// a laptop's window, one screen after another, and writes for each how wide
// its content is drawn and which of the rail's own actions its toolbar shows
// as well; then the same of the list screen in the page itself, as narrow as
// a phone's, where there is no rail. Each frame runs this probe too, being
// the same script; it is held open like the page, so it is told it is done.
const laptopProbe = `(function probe() {
  if (window !== window.top) return;
  const rail = ['New conversation', 'Search'];
  const shown = d => [...d.querySelectorAll('#view button')].filter(b => b.getClientRects().length && rail.includes(b.textContent.trim())).map(b => b.textContent.trim());
  const ready = d => { const v = d && d.getElementById('view'); return v && v.children.length && v.getAttribute('aria-busy') !== 'true'; };
  const seen = [];
  const screens = (new URLSearchParams(location.search).get('screens') || '').split(',');
  const f = document.createElement('iframe');
  f.style.width = '1440px'; f.style.height = '900px'; f.style.border = '0';
  document.body.append(f);
  const write = () => {
    seen.push('phone sessions: repeats=' + shown(document).join('+'));
    const out = document.createElement('pre');
    out.textContent = 'laptop: ' + seen.join(' ; ') + ' |';
    document.body.prepend(out);
  };
  (function next(i) {
    if (i >= screens.length) return write();
    f.onload = () => (function wait(left) {
      const d = f.contentDocument;
      if (left && !ready(d)) { fetch('/held').then(() => wait(left - 1)); return; }
      const v = d.getElementById('view');
      seen.push(screens[i] + ': width=' + Math.round(v.getBoundingClientRect().width) + ' repeats=' + shown(d).join('+'));
      d.body.append(d.createTextNode('laptop: done'));
      next(i + 1);
    })(200);
    f.src = '/?n=' + i + '#' + screens[i];
  })(0);
})();`

// On a laptop the rail beside every screen offers a new conversation and a
// search, and a screen's own toolbar does not offer them again; a transcript
// and a screen read from top to bottom are held to a reading column, and a
// list takes the width there is (webui.html, "Shell"). In a real browser
// against the interface as shipped, in a frame as wide as a laptop's window.
func TestALaptopScreenRepeatsNothingTheRailOffers(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "laptop:", laptopProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s := newSession(t, a)

	screens := []string{"sessions", "jobs", "skills", "session/" + s.ID, "new", "tools", "panels"}
	got := browseFor(t, a, srv, s.ID, "?screens="+strings.Join(screens, ",")+"#sessions", "laptop:")
	widths := map[string]int{}
	for _, m := range regexp.MustCompile(`([\w/]+): width=(\d+) repeats=([^;|]*)`).FindAllStringSubmatch(got, -1) {
		w, _ := strconv.Atoi(m[2])
		widths[m[1]] = w
		if strings.TrimSpace(m[3]) != "" {
			t.Errorf("on a laptop %s repeats the rail's %s", m[1], m[3])
		}
	}
	if len(widths) != len(screens) {
		t.Fatalf("the probe measured %d of %d screens: %s", len(widths), len(screens), got)
	}
	for _, list := range []string{"sessions", "jobs", "skills"} {
		if widths[list] <= 900 {
			t.Errorf("the %s list is held to a column (%dpx) rather than the width there is: %s", list, widths[list], got)
		}
	}
	for _, read := range []string{"session/" + s.ID, "new", "tools", "panels"} {
		if widths[read] > 900 {
			t.Errorf("%s, read from top to bottom, is %dpx wide rather than a reading column: %s", read, widths[read], got)
		}
	}
	if !strings.Contains(got, "phone sessions: repeats=New conversation+Search |") {
		t.Errorf("on a phone, where there is no rail, the list's toolbar must offer both: %s", got)
	}
}
