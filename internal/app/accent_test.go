package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The accent is the operator's choice among a few colours that read well in
// light and dark (webui.html, "Look"). The agent keeps it, so every device the
// operator opens shows the same one, and a restart does not lose it.

type accentPrefs struct {
	Model   string   `json:"model"`
	Accent  string   `json:"accent"`
	Accents []string `json:"accents"`
}

func readPrefs(t *testing.T, a *App) accentPrefs {
	t.Helper()
	w := call(t, a, "GET", "/preferences", "")
	if w.Code != 200 {
		t.Fatalf("GET /preferences: %d %s", w.Code, w.Body.String())
	}
	var p accentPrefs
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return p
}

func TestTheAccentIsCopperUntilOneIsChosen(t *testing.T) {
	a := newTestApp(t)
	p := readPrefs(t, a)
	if p.Accent != "copper" {
		t.Errorf("accent with nothing chosen = %q, want copper", p.Accent)
	}
	if want := []string{"copper", "ink", "plum", "graphite"}; !slices.Equal(p.Accents, want) {
		t.Errorf("accents offered = %v, want %v", p.Accents, want)
	}
}

func TestTheChosenAccentIsKeptByTheAgentAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	first := newTestAppAt(t, dir)
	decodeSession(t, call(t, first, "POST", "/sessions", `{"model":"x/chosen"}`))

	w := call(t, first, "PUT", "/preferences/accent", `{"accent":"plum"}`)
	if w.Code != 200 {
		t.Fatalf("PUT /preferences/accent: %d %s", w.Code, w.Body.String())
	}
	if got := readPrefs(t, first); got.Accent != "plum" {
		t.Errorf("after choosing plum the accent is %q", got.Accent)
	}

	second := newTestAppAt(t, dir)
	got := readPrefs(t, second)
	if got.Accent != "plum" {
		t.Errorf("after a restart the accent is %q, want plum", got.Accent)
	}
	if got.Model != "x/chosen" {
		t.Errorf("choosing an accent changed the model a conversation starts on to %q", got.Model)
	}
}

func TestAnAccentNotOfferedIsRefusedByName(t *testing.T) {
	a := newTestApp(t)
	call(t, a, "PUT", "/preferences/accent", `{"accent":"ink"}`)

	for _, body := range []string{`{"accent":"neon"}`, `{"accent":""}`, `not json`} {
		w := call(t, a, "PUT", "/preferences/accent", body)
		if w.Code != 400 {
			t.Errorf("PUT %s: status %d, want 400", body, w.Code)
		}
		if !strings.Contains(w.Body.String(), "copper, ink, plum, graphite") {
			t.Errorf("PUT %s: the refusal does not name the choices: %s", body, w.Body.String())
		}
	}
	if got := readPrefs(t, a).Accent; got != "ink" {
		t.Errorf("a refused accent changed the choice to %q", got)
	}
}

// Every accent is drawn in both schemes: one block that gives it its light
// values, and one inside the dark scheme that gives it its dark ones. The
// block names the accent alone, not the page, so a swatch that carries the
// name is drawn in that accent whichever one the page is in.
func TestEveryAccentIsDrawnInBothSchemes(t *testing.T) {
	css, err := os.ReadFile("../../web/style.css")
	if err != nil {
		t.Fatal(err)
	}
	dark := darkBlock(t, string(css))
	for _, name := range accents {
		sel := `[data-accent="` + name + `"]`
		light := strings.Replace(string(css), dark, "", 1)
		if !strings.Contains(light, sel) {
			t.Errorf("%s has no light values in style.css (%s)", name, sel)
		}
		if !strings.Contains(dark, sel) {
			t.Errorf("%s has no dark values in style.css's dark scheme", name)
		}
	}
}

// darkBlock is the stylesheet's dark scheme: the media block that redefines the
// tokens, up to the brace that closes it.
func darkBlock(t *testing.T, css string) string {
	t.Helper()
	start := strings.Index(css, "@media (prefers-color-scheme: dark)")
	if start < 0 {
		t.Fatal("style.css has no dark scheme")
	}
	depth := 0
	for i := start; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[start : i+1]
			}
		}
	}
	t.Fatal("style.css's dark scheme never closes")
	return ""
}

// accentProbe waits until the interface has put an accent on the page, then
// writes which one and the colour the browser computed for it at the top.
const accentProbe = `(function probe(left) {
  const root = document.documentElement;
  if (left && !root.dataset.accent) { fetch('/held').then(() => probe(left - 1)); return; }
  const out = document.createElement('pre');
  out.textContent = 'accent: ' + (root.dataset.accent || 'none') + ' ' + getComputedStyle(root).getPropertyValue('--accent').trim();
  document.body.prepend(out);
})(200);`

// The accent chosen is the one a browser opening the interface draws, in the
// colour the stylesheet gives it.
func TestTheChosenAccentColoursTheInterfaceInABrowser(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "accent:", accentProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)

	if w := call(t, a, "PUT", "/preferences/accent", `{"accent":"plum"}`); w.Code != 200 {
		t.Fatalf("PUT /preferences/accent: %d %s", w.Code, w.Body.String())
	}
	css, err := os.ReadFile("../../web/style.css")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`\[data-accent="plum"\]\s*\{[^}]*--accent:\s*(#[0-9a-f]+)`).FindStringSubmatch(string(css))
	if m == nil {
		t.Fatal("style.css gives plum no --accent")
	}

	s := newSession(t, a)
	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/"})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	if want := "accent: plum " + m[1]; !strings.Contains(seen.Content, want) {
		t.Errorf("the browser drew %q, want %q", regexp.MustCompile(`accent: [^\n]*`).FindString(seen.Content), want)
	}
}
