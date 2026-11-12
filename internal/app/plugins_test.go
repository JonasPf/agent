package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipEntry is one entry of an archive a test puts together.
type zipEntry struct {
	name string
	body []byte
	mode os.FileMode
}

func zipOf(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// mcpb is the stand-in bundle as a release publishes one: a zip holding
// manifest.json and the server, executable.
func mcpb(t *testing.T, manifest string, more ...zipEntry) []byte {
	t.Helper()
	bin, err := os.ReadFile(shellServer(t))
	if err != nil {
		t.Fatal(err)
	}
	return zipOf(t, append([]zipEntry{
		{name: "manifest.json", body: []byte(manifest)},
		{name: "server/shell", body: bin, mode: 0o755},
	}, more...)...)
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// sendRaw puts a body to the API as it is, the way a browser sends a file.
func sendRaw(t *testing.T, a *App, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/zip")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

// sendJSON is callAPI saying what it sends, which a route that also takes a
// file needs to be told.
func sendJSON(t *testing.T, a *App, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

// pluginApp is a confined agent with a directory of bundles the image ships
// and an empty one for what the operator installs.
func pluginApp(t *testing.T) (*App, string, string) {
	t.Helper()
	a, _ := confinedApp(t, t.TempDir(), "", "")
	image, installed := t.TempDir(), filepath.Join(t.TempDir(), "plugins")
	a.cfg.PluginsDir, a.cfg.UserPluginsDir = image, installed
	a.tools.bundleDirs = []string{image, installed}
	return a, image, installed
}

func pluginsOf(t *testing.T, a *App) map[string]Plugin {
	t.Helper()
	w := callAPI(t, a, "GET", "/plugins", nil)
	if w.Code != 200 {
		t.Fatalf("GET /plugins = %d %s", w.Code, w.Body)
	}
	var out struct {
		Plugins []Plugin `json:"plugins"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	by := map[string]Plugin{}
	for _, p := range out.Plugins {
		by[p.Name] = p
	}
	return by
}

func withConfig(manifest string) string {
	manifest = strings.Replace(manifest, `"FROM_MANIFEST": "${__dirname}"`,
		`"FROM_MANIFEST": "${__dirname}", "TOKEN": "${user_config.token}", "REGION": "${user_config.region}"`, 1)
	return strings.Replace(manifest, `"compatibility"`,
		`"user_config": {"token": {"type": "string", "title": "Token", "description": "The service's key.", "required": true, "sensitive": true},
		 "region": {"type": "string", "title": "Region", "default": "eu"}}, "compatibility"`, 1)
}

// "A bundle is installed in two requests: the first says what it is and what it
// asks for and installs nothing, the second installs what the first described."
// And then: "a value the bundle asks for is set through the API and never
// served back", "a bundle that needs a value it does not have offers no tools",
// "removing a bundle removes its tools and its values".
func TestABundleIsInstalledConfiguredCalledAndRemovedThroughTheAPI(t *testing.T) {
	a, _, installed := pluginApp(t)
	archive := mcpb(t, withConfig(shellManifest))

	w := sendRaw(t, a, "POST", "/plugins/inspect", archive)
	if w.Code != 200 {
		t.Fatalf("inspect = %d %s", w.Code, w.Body)
	}
	var seen struct {
		Plugin
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &seen); err != nil {
		t.Fatal(err)
	}
	if seen.Name != "shell" || seen.Version != "1.2.3" || seen.Description != "A shell, for the tests." {
		t.Errorf("inspect names %q %q %q, want the manifest's", seen.Name, seen.Version, seen.Description)
	}
	if len(seen.Tools) != 1 || seen.Tools[0] != "sh" {
		t.Errorf("inspect lists tools %v, want the server's", seen.Tools)
	}
	if seen.SHA256 != sumOf(archive) {
		t.Errorf("inspect sha256 = %q, want the archive's", seen.SHA256)
	}
	if seen.Timeout != 2 || len(seen.Config) != 2 || len(seen.Needs) != 1 || seen.Needs[0] != "token" {
		t.Errorf("inspect = %+v, want its timeout, both settings and the one it needs", seen.Plugin)
	}
	if entries, _ := os.ReadDir(installed); len(entries) != 0 {
		t.Errorf("inspect left %d entries where bundles are installed", len(entries))
	}
	if len(pluginsOf(t, a)) != 0 {
		t.Error("inspect installed the bundle")
	}

	// The archive that is installed is the one that was looked at.
	if w := sendRaw(t, a, "POST", "/plugins?sha256="+strings.Repeat("0", 64), archive); w.Code != 409 {
		t.Errorf("install with another archive's sum = %d, want 409", w.Code)
	}
	if w := sendRaw(t, a, "POST", "/plugins", archive); w.Code != 400 {
		t.Errorf("install with no sum = %d, want 400", w.Code)
	}
	if w := sendRaw(t, a, "POST", "/plugins?sha256="+seen.SHA256, archive); w.Code != 201 {
		t.Fatalf("install = %d %s", w.Code, w.Body)
	}
	p := pluginsOf(t, a)["shell"]
	if p.Source != "installed" || len(p.Needs) != 1 || p.Failure != "" {
		t.Errorf("after install = %+v, want it installed and needing its token", p)
	}
	if tools, _ := listedTools(t, a); tools["sh"] != nil {
		t.Error("a bundle that needs a value it does not have offers its tool")
	}

	if w := sendJSON(t, a, "PUT", "/plugins/shell/config", map[string]any{"nope": "x"}); w.Code != 400 {
		t.Errorf("a setting the bundle does not ask for = %d, want 400", w.Code)
	}
	w = sendJSON(t, a, "PUT", "/plugins/shell/config", map[string]any{"token": "s3cret"})
	if w.Code != 200 {
		t.Fatalf("config = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "s3cret") {
		t.Errorf("the value was served back: %s", w.Body)
	}
	if w := callAPI(t, a, "GET", "/plugins", nil); strings.Contains(w.Body.String(), "s3cret") {
		t.Errorf("GET /plugins serves the value: %s", w.Body)
	}
	p = pluginsOf(t, a)["shell"]
	set := map[string]bool{}
	for _, c := range p.Config {
		set[c.Key] = c.Set
	}
	if len(p.Needs) != 0 || !set["token"] || set["region"] {
		t.Errorf("after config = %+v, want token set, region not, nothing needed", p)
	}
	kept := filepath.Join(a.cfg.DataDir, "plugins.json")
	if st, err := os.Stat(kept); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("%s: %v, mode %v; want it kept beside the data, readable by the agent alone", kept, err, st)
	}

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	call := func() toolResult {
		args, _ := json.Marshal(map[string]string{"command": "echo token=$TOKEN region=$REGION"})
		return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "sh", args)
	}
	if res := call(); !res.OK || !strings.Contains(res.Content, "token=s3cret region=eu") {
		t.Errorf("call = %+v, want the value set and the manifest's default", res)
	}

	// A newer version takes the old one's place and keeps its values.
	newer := mcpb(t, strings.Replace(withConfig(shellManifest), "1.2.3", "1.3.0", 1))
	if w := sendRaw(t, a, "POST", "/plugins?sha256="+sumOf(newer), newer); w.Code != 201 {
		t.Fatalf("installing a newer version = %d %s", w.Code, w.Body)
	}
	if p := pluginsOf(t, a)["shell"]; p.Version != "1.3.0" || len(p.Needs) != 0 {
		t.Errorf("after the newer version = %+v, want 1.3.0 with its token kept", p)
	}

	if w := sendJSON(t, a, "PUT", "/plugins/shell/config", map[string]any{"token": nil}); w.Code != 200 {
		t.Fatalf("clearing = %d %s", w.Code, w.Body)
	}
	if res := call(); res.OK {
		t.Errorf("call after the value was cleared = %+v, want no such tool", res)
	}
	sendJSON(t, a, "PUT", "/plugins/shell/config", map[string]any{"token": "s3cret"})

	if w := callAPI(t, a, "DELETE", "/plugins/shell", nil); w.Code != 204 {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	if len(pluginsOf(t, a)) != 0 {
		t.Error("the bundle is still listed")
	}
	if tools, _ := listedTools(t, a); tools["sh"] != nil {
		t.Error("a removed bundle's tool is still offered")
	}
	if b, _ := os.ReadFile(kept); strings.Contains(string(b), "s3cret") {
		t.Error("a removed bundle's values are still kept")
	}
	if w := callAPI(t, a, "DELETE", "/plugins/shell", nil); w.Code != 404 {
		t.Errorf("deleting what is not there = %d, want 404", w.Code)
	}
}

// "A bundle the image ships is configured like any other and cannot be removed
// or replaced through the API."
func TestABundleTheImageShipsIsNotReplacedOrRemoved(t *testing.T) {
	a, image, _ := pluginApp(t)
	writeBundle(t, image, "shell", shellManifest)
	a.ReloadTools("")
	if p := pluginsOf(t, a)["shell"]; p.Source != "image" || len(p.Tools) != 1 {
		t.Fatalf("plugins = %+v, want the shipped bundle", p)
	}
	if w := callAPI(t, a, "DELETE", "/plugins/shell", nil); w.Code != 409 {
		t.Errorf("delete = %d, want 409", w.Code)
	}
	archive := mcpb(t, strings.Replace(shellManifest, "1.2.3", "9.9.9", 1))
	w := sendRaw(t, a, "POST", "/plugins?sha256="+sumOf(archive), archive)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "ships with") {
		t.Errorf("installing over it = %d %s, want 409 saying it ships with the agent", w.Code, w.Body)
	}
	// Another bundle offering a tool that is already there.
	twin := mcpb(t, strings.Replace(shellManifest, `"name": "shell"`, `"name": "twin"`, 1))
	w = sendRaw(t, a, "POST", "/plugins?sha256="+sumOf(twin), twin)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "sh") {
		t.Errorf("installing a second sh = %d %s, want 409 naming the tool", w.Code, w.Body)
	}
	if p := pluginsOf(t, a); len(p) != 1 || p["shell"].Version != "1.2.3" {
		t.Errorf("plugins = %+v, want only the shipped one", p)
	}
}

// "An archive that would write outside its own folder, or is larger than a
// bundle may be, is refused, and a refused install leaves the version that was
// there."
func TestAnArchiveThatCannotBeInstalledLeavesWhatWasThere(t *testing.T) {
	a, _, installed := pluginApp(t)
	good := mcpb(t, shellManifest)
	if w := sendRaw(t, a, "POST", "/plugins?sha256="+sumOf(good), good); w.Code != 201 {
		t.Fatalf("install = %d %s", w.Code, w.Body)
	}
	outside := filepath.Join(filepath.Dir(installed), "planted")
	next := strings.Replace(shellManifest, "1.2.3", "2.0.0", 1)
	// Larger than the server, which has to fit under the limit set below.
	big := make([]byte, 33<<20)
	old := maxBundleFile
	t.Cleanup(func() { maxBundleFile = old })
	for name, archive := range map[string][]byte{
		"climbs":     mcpb(t, next, zipEntry{name: "../planted", body: []byte("x")}),
		"absolute":   mcpb(t, next, zipEntry{name: outside, body: []byte("x")}),
		"link":       mcpb(t, next, zipEntry{name: "server/link", body: []byte(outside), mode: os.ModeSymlink | 0o777}),
		"not a zip":  []byte("this is not an archive"),
		"cannot run": mcpb(t, strings.Replace(next, `"type": "binary"`, `"type": "node"`, 1)),
	} {
		w := sendRaw(t, a, "POST", "/plugins?sha256="+sumOf(archive), archive)
		if w.Code < 400 || w.Code >= 500 {
			t.Errorf("%s: install = %d %s, want it refused", name, w.Code, w.Body)
		}
	}
	maxBundleFile = 32 << 20
	if w := sendRaw(t, a, "POST", "/plugins/inspect", mcpb(t, next, zipEntry{name: "big", body: big})); w.Code < 400 || !strings.Contains(w.Body.String(), "big") {
		t.Errorf("a file past the limit = %d %s, want it refused by name", w.Code, w.Body)
	}
	maxBundleFile = old
	if _, err := os.Stat(outside); err == nil {
		t.Error("an archive wrote outside the directory bundles are installed in")
	}
	if p := pluginsOf(t, a); len(p) != 1 || p["shell"].Version != "1.2.3" || p["shell"].Failure != "" {
		t.Errorf("plugins = %+v, want the version that was there, working", p)
	}
	entries, _ := os.ReadDir(installed)
	if len(entries) != 1 {
		t.Errorf("%d entries where bundles are installed, want the one bundle and nothing left over", len(entries))
	}
}

// "A bundle can be installed from an https address; the agent fetches it."
func TestABundleInstallsFromAnAddress(t *testing.T) {
	a, _, _ := pluginApp(t)
	archive := mcpb(t, shellManifest)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shell.mcpb" {
			http.NotFound(w, r)
			return
		}
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	a.fetch = srv.Client()

	w := sendJSON(t, a, "POST", "/plugins/inspect", map[string]string{"url": srv.URL + "/shell.mcpb"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), sumOf(archive)) {
		t.Fatalf("inspect by address = %d %s", w.Code, w.Body)
	}
	if w := sendJSON(t, a, "POST", "/plugins?sha256="+sumOf(archive), map[string]string{"url": srv.URL + "/shell.mcpb"}); w.Code != 201 {
		t.Fatalf("install by address = %d %s", w.Code, w.Body)
	}
	if p := pluginsOf(t, a)["shell"]; p.Source != "installed" || len(p.Tools) != 1 {
		t.Errorf("plugins = %+v, want the bundle installed", p)
	}
	for _, url := range []string{strings.Replace(srv.URL, "https://", "http://", 1) + "/shell.mcpb", srv.URL + "/missing", "file:///etc/passwd"} {
		if w := sendJSON(t, a, "POST", "/plugins/inspect", map[string]string{"url": url}); w.Code != 400 {
			t.Errorf("%s: inspect = %d %s, want 400", url, w.Code, w.Body)
		}
	}
}

const zippedSkill = "---\nname: zipped\ndescription: Use when a test installs a skill from an archive.\n---\n\nRead `notes/more.md` when it matters.\n"

// "A skill is installed from an archive of its folder, uploaded or fetched
// from an https address, by the rules a drafted one is."
func TestASkillInstallsFromAnArchive(t *testing.T) {
	a := newTestApp(t)
	// As `zip -r zipped.zip zipped/` makes one: the folder is in the archive.
	archive := zipOf(t,
		zipEntry{name: "zipped/SKILL.md", body: []byte(zippedSkill)},
		zipEntry{name: "zipped/notes/more.md", body: []byte("More.\n")})
	w := sendRaw(t, a, "POST", "/skills/install", archive)
	if w.Code != 201 {
		t.Fatalf("install = %d %s", w.Code, w.Body)
	}
	sk := a.skills.Get("zipped")
	if sk == nil || len(sk.Files) != 1 || sk.Files[0].Path != "notes/more.md" {
		t.Fatalf("skill = %+v, want it installed with its file", sk)
	}
	if w := sendRaw(t, a, "POST", "/skills/install", archive); w.Code != 409 {
		t.Errorf("installing it again = %d, want 409 until replace is said", w.Code)
	}

	// The files at the top of the archive, fetched from an address.
	flat := zipOf(t,
		zipEntry{name: "SKILL.md", body: []byte(strings.Replace(zippedSkill, "archive.", "address.", 1))},
		zipEntry{name: "notes/more.md", body: []byte("More.\n")})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(flat) }))
	t.Cleanup(srv.Close)
	a.fetch = srv.Client()
	w = sendJSON(t, a, "POST", "/skills/install", map[string]any{"url": srv.URL + "/zipped.zip", "replace": true})
	if w.Code != 201 {
		t.Fatalf("install by address = %d %s", w.Code, w.Body)
	}
	if sk := a.skills.Get("zipped"); sk == nil || !strings.Contains(sk.Description, "address") {
		t.Errorf("skill = %+v, want the fetched one in its place", sk)
	}

	for name, bad := range map[string][]byte{
		"no SKILL.md": zipOf(t, zipEntry{name: "notes.md", body: []byte("x")}),
		"not text":    zipOf(t, zipEntry{name: "SKILL.md", body: []byte(strings.Replace(zippedSkill, "zipped", "binary", 1))}, zipEntry{name: "a.bin", body: []byte{0xff, 0xfe, 0x00}}),
		"climbs":      zipOf(t, zipEntry{name: "SKILL.md", body: []byte(strings.Replace(zippedSkill, "zipped", "climbs", 1))}, zipEntry{name: "../x.md", body: []byte("x")}),
	} {
		if w := sendRaw(t, a, "POST", "/skills/install", bad); w.Code != 400 {
			t.Errorf("%s: install = %d %s, want 400", name, w.Code, w.Body)
		}
	}
	if a.skills.Get("binary") != nil || a.skills.Get("climbs") != nil {
		t.Error("a refused archive installed a skill")
	}
}
