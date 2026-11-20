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

// filesProbe waits for the files screen, writes the names of the rows at the
// top of the tree, then swipes the first file's row to the left as a finger
// would, clicks where the swipe ended, and writes whether the row slid open
// and whether the file was opened.
const filesProbe = `(function probe(left) {
  const rows = [...document.querySelectorAll('#view .tree .row-item')];
  if (left && !rows.length) { fetch('/held').then(() => probe(left - 1)); return; }
  let opened = false;
  window.open = () => { opened = true; };
  const top = rows.filter(r => r.dataset.depth === '0').map(r => r.querySelector('.n').textContent);
  const file = rows.find(r => !r.classList.contains('dir'));
  const wrap = file.closest('.row-wrap');
  const r = file.getBoundingClientRect();
  const y = r.top + r.height / 2;
  const touch = (type, x) => file.dispatchEvent(new PointerEvent(type, { bubbles: true, clientX: x, clientY: y,
    pointerId: 1, pointerType: 'touch', isPrimary: true }));
  touch('pointerdown', r.right - 20);
  touch('pointermove', r.right - 60);
  touch('pointermove', r.right - 140);
  touch('pointerup', r.right - 140);
  file.click();
  const out = document.createElement('pre');
  out.textContent = 'files: top=' + top.join(',') + ' ; open=' + !!(wrap && wrap.classList.contains('open')) +
    ' ; opened=' + opened + ' |';
  document.body.prepend(out);
})(200);`

// sandboxProbe waits for the Plugins screen, writes whether the sandbox is
// folded and what its one line says, then opens it and writes how many rows
// it holds.
const sandboxProbe = `(function probe(left) {
  const box = document.querySelector('#view details.sandbox');
  if (left && !box) { fetch('/held').then(() => probe(left - 1)); return; }
  const line = box.querySelector('summary').textContent.replace(/\s+/g, ' ').trim();
  const folded = !box.open;
  box.open = true;
  const out = document.createElement('pre');
  out.textContent = 'sandbox: folded=' + folded + ' ; line=' + line + ' ; rows=' +
    box.querySelectorAll('.kv').length + ' |';
  document.body.prepend(out);
})(200);`

// The files screen leaves out the tools' own folders while they are empty and
// lists them last once they hold something, and a file's delete is behind a
// swipe (webui.html, "Files"). In a real browser against the interface as
// shipped. The folders are made as a tool's run makes them; the browsing runs
// from another conversation, since the browser keeps its own scratch in the
// .tmp of the conversation it runs in.
func TestTheFilesScreenLeavesOutEmptyToolFoldersAndSwipesToDelete(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "files:", filesProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s, browser := newSession(t, a), newSession(t, a)
	ws := a.ensureWorkspace(s.ID)
	if err := os.WriteFile(filepath.Join(ws, "notes.md"), []byte("# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{a.sandbox.HomeDir(ws), a.sandbox.TempDir(ws)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	browse := func() string {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"url": srv.URL + "/#files/" + s.ID})
		seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: browser.ID}, "web_browse", args)
		if !seen.OK {
			t.Fatalf("web_browse: %s", seen.Error)
		}
		m := regexp.MustCompile(`files: ([^\n]*)`).FindStringSubmatch(seen.Content)
		if m == nil {
			t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
		}
		return m[1]
	}

	got := browse()
	for _, want := range []string{"top=notes.md ;", "open=true", "opened=false"} {
		if !strings.Contains(got, want) {
			t.Errorf("with the tools' folders empty: want %q in %s", want, got)
		}
	}
	if err := os.WriteFile(filepath.Join(a.sandbox.TempDir(ws), "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := browse(); !strings.Contains(got, "top=notes.md,.tmp/ ;") {
		t.Errorf("with something in .tmp: want it listed last, after notes.md, in %s", got)
	}
}

// The sandbox is one folded line on the Plugins screen, saying what enforces
// it, and opens to its rows (webui.html, "Panels"). In a real browser against
// the interface as shipped.
func TestThePluginsScreenFoldsTheSandboxIntoOneLine(t *testing.T) {
	browserOrSkip(t)
	a := browseApp(t)
	a.cfg.WebDir = "../../web"
	a.sched = NewScheduler(a)
	srv := httptest.NewServer(heldUntil(t, a.routes(), "sandbox:", sandboxProbe))
	t.Cleanup(srv.Close)
	allowServer(t, a, srv.URL)
	a.or = NewOpenRouter("test-key")
	a.or.base = scriptedModel(t, toolStep{text: "Noted."}).URL
	s := newSession(t, a)

	args, _ := json.Marshal(map[string]string{"url": srv.URL + "/#tools"})
	seen := a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "web_browse", args)
	if !seen.OK {
		t.Fatalf("web_browse: %s", seen.Error)
	}
	m := regexp.MustCompile(`sandbox: ([^\n]*)`).FindStringSubmatch(seen.Content)
	if m == nil {
		t.Fatalf("the probe wrote nothing; the browser read:\n%s", seen.Content)
	}
	got := m[1]
	reach := a.sandbox.Reach()
	folded := "folded=true"
	if !reach.Enforced || !reach.NetworkEnforced {
		folded = "folded=false"
	}
	for _, want := range []string{folded, "The sandbox", "enforced"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in %s", want, got)
		}
	}
	if !regexp.MustCompile(`rows=[4-9]`).MatchString(got) {
		t.Errorf("the sandbox opened to fewer rows than its paths, devices and network: %s", got)
	}
}
