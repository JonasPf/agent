package app

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// foldProbe waits for the conversation's runs of tool calls and its answer,
// writes how each run stands — marked, its line, folded or not — then taps
// the first run's line and writes whether that opened it.
const foldProbe = `(function probe(left) {
  const runs = document.querySelectorAll('.work');
  if (left && (!runs.length || !document.querySelector('.msg.agent'))) { fetch('/held').then(() => probe(left - 1)); return; }
  const seen = [...runs].map(w => 'class=' + w.className + ' sum=' + w.querySelector('.sum').textContent +
    ' folded=' + w.querySelector('.steps').hidden);
  const first = runs[0];
  if (first) first.querySelector('.work-line').click();
  const out = document.createElement('pre');
  out.textContent = 'work: ' + seen.join('; ') + ' | after=' + (first ? first.querySelector('.steps').hidden : '-') + ' |';
  document.body.prepend(out);
})(200);`

// A turn's tool calls are folded into one line that names what ran and opens
// on a tap; a run with a failed call is open, marked, and names the failure
// (webui.html, "Conversation view"). In a real browser against the interface
// as shipped, with the tools as they run.
func TestATurnsToolCallsFoldIntoOneLineThatOpens(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "work:", foldProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")

	turn := func(steps ...toolStep) *Session {
		a.or.base = scriptedModel(t, steps...).URL
		s := newSession(t, a)
		if err := a.SendUserMessage(s.ID, "go on"); err != nil {
			t.Fatal(err)
		}
		settle(t, a, s.ID)
		return s
	}
	fine := turn(toolStep{tool: "session_search", args: `{"query":"lisbon"}`}, toolStep{text: "Nothing about Lisbon."})
	failing := turn(toolStep{tool: "skill_read", args: `{"name":"no-such-skill"}`}, toolStep{text: "There is no such skill."})

	browse := func(s *Session) string {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"url": srv.URL + "/#session/" + s.ID})
		seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
		if !seen.OK {
			t.Fatalf("web_browse: %s", seen.Error)
		}
		m := regexp.MustCompile(`work: ([^\n]*)`).FindStringSubmatch(seen.Content)
		if m == nil {
			t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
		}
		return m[1]
	}

	got := browse(fine)
	for _, want := range []string{"sum=Ran session_search", "folded=true", "after=false"} {
		if !strings.Contains(got, want) {
			t.Errorf("a run that went well: want %q in %s", want, got)
		}
	}
	if strings.Contains(got, "failed") {
		t.Errorf("a run that went well is marked failed: %s", got)
	}

	got = browse(failing)
	for _, want := range []string{"class=work failed", "sum=skill_read failed: no such skill", "folded=false"} {
		if !strings.Contains(got, want) {
			t.Errorf("a run with a failure: want %q in %s", want, got)
		}
	}
}
