package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// upload puts one file into a session through the gateway, the way the
// interface does.
func upload(t *testing.T, a *App, sessionID, name, content string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/sessions/"+sessionID+"/files", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	return w
}

// catTool registers a real tool that prints a file from the directory it runs
// in, so what a tool sees is observed from a subprocess rather than asserted
// about a variable.
func catTool(t *testing.T, a *App, file string) {
	t.Helper()
	dir := t.TempDir()
	writeTool(t, dir, "catter", "#!/bin/sh\nprintf '{\"ok\":true,\"content\":\"%s\"}' \"$(cat "+file+" 2>/dev/null)\"\n")
	a.tools.dir = dir
	if _, f := a.tools.Load(a); len(f) > 0 {
		t.Fatalf("load failures: %v", f)
	}
}

// callTool runs a tool through the gateway in a session's context and returns
// what it printed.
func callTool(t *testing.T, a *App, sessionID, name string) toolResult {
	t.Helper()
	req := httptest.NewRequest("POST", "/tools/"+name+"/call?session_id="+sessionID, strings.NewReader("{}"))
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("tool call status = %d: %s", w.Code, w.Body.String())
	}
	var res toolResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func newSession(t *testing.T, a *App) *Session {
	t.Helper()
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A file uploaded to a session lands in that session's working directory, which
// is where its tools run.
func TestUploadLandsInTheSessionWorkingDirectory(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	catTool(t, a, "notes.txt")

	w := upload(t, a, s.ID, "notes.txt", "hello upload")
	if w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	var up struct {
		Path  string `json:"path"`
		Bytes int64  `json:"bytes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	if up.Path != "notes.txt" || up.Bytes != 12 {
		t.Errorf("upload reported %+v, want notes.txt and 12 bytes", up)
	}
	if res := callTool(t, a, s.ID, "catter"); res.Content != "hello upload" {
		t.Errorf("tool read %q, want the uploaded file", res.Content)
	}
}

// One session's files are not another's: a working directory belongs to the
// conversation that owns it.
func TestSessionsHaveSeparateWorkingDirectories(t *testing.T) {
	a := newTestApp(t)
	one, two := newSession(t, a), newSession(t, a)
	catTool(t, a, "notes.txt")

	if w := upload(t, a, one.ID, "notes.txt", "only mine"); w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	if res := callTool(t, a, one.ID, "catter"); res.Content != "only mine" {
		t.Errorf("owning session read %q", res.Content)
	}
	if res := callTool(t, a, two.ID, "catter"); res.Content != "" {
		t.Errorf("other session read %q, want nothing", res.Content)
	}
}

// A fork continues the conversation, so it continues its files too: the
// successor starts from a copy, and writing in one does not reach the other.
func TestForkCopiesTheWorkingDirectory(t *testing.T) {
	a := newTestApp(t)
	pred := newSession(t, a)
	catTool(t, a, "notes.txt")
	if w := upload(t, a, pred.ID, "notes.txt", "carried across"); w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+pred.ID+"/fork",
		strings.NewReader(`{"archive":false}`)))
	if w.Code != 201 {
		t.Fatalf("fork status = %d: %s", w.Code, w.Body.String())
	}
	var succ Session
	if err := json.Unmarshal(w.Body.Bytes(), &succ); err != nil {
		t.Fatal(err)
	}
	if res := callTool(t, a, succ.ID, "catter"); res.Content != "carried across" {
		t.Errorf("fork read %q, want the predecessor's file", res.Content)
	}

	if w := upload(t, a, succ.ID, "notes.txt", "changed here"); w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	if res := callTool(t, a, pred.ID, "catter"); res.Content != "carried across" {
		t.Errorf("predecessor read %q; a copy is not a share", res.Content)
	}
}

// Uploads are deleted with the session that holds them.
func TestDeletingASessionRemovesItsWorkingDirectory(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	if w := upload(t, a, s.ID, "notes.txt", "gone soon"); w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	dir := a.sessionWorkspace(s.ID)
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("DELETE", "/sessions/"+s.ID, nil))
	if w.Code != 204 {
		t.Fatalf("delete status = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("working directory survived the session: %v", err)
	}
}

// The files a session holds are listed and readable back, so an upload can be
// confirmed and a file the agent wrote can be fetched.
func TestSessionFilesAreListedAndReadable(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	if w := upload(t, a, s.ID, "notes.txt", "hello"); w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+s.ID+"/files", nil))
	if w.Code != 200 {
		t.Fatalf("list status = %d: %s", w.Code, w.Body.String())
	}
	var files []SessionFile
	if err := json.Unmarshal(w.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "notes.txt" || files[0].Bytes != 5 {
		t.Fatalf("listing = %+v, want one notes.txt of 5 bytes", files)
	}

	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+s.ID+"/files/notes.txt", nil))
	if w.Code != 200 || w.Body.String() != "hello" {
		t.Fatalf("download = %d %q", w.Code, w.Body.String())
	}
}

// A tool that clones a repository or unpacks an archive leaves directories, some
// of them empty. The listing names each one, so the files screen can show the
// tree the agent is actually working in rather than a flat pile of paths.
func TestSessionFilesNameTheirDirectories(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	dir := t.TempDir()
	writeTool(t, dir, "maker", "#!/bin/sh\nmkdir -p src/empty && printf hi > src/main.go && "+
		"printf '{\"ok\":true,\"content\":\"made\"}'\n")
	a.tools.dir = dir
	if _, f := a.tools.Load(a); len(f) > 0 {
		t.Fatalf("load failures: %v", f)
	}
	if res := callTool(t, a, s.ID, "maker"); res.Content != "made" {
		t.Fatalf("maker said %q", res.Content)
	}

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+s.ID+"/files", nil))
	if w.Code != 200 {
		t.Fatalf("list status = %d: %s", w.Code, w.Body.String())
	}
	var files []SessionFile
	if err := json.Unmarshal(w.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = f.Dir
	}
	// .tmp is where the tool's scratch files went and .home is its home
	// directory: both listed like anything else the session wrote, and neither
	// is what this test is about.
	delete(got, ".tmp")
	delete(got, ".home")
	want := map[string]bool{"src": true, "src/empty": true, "src/main.go": false}
	if len(got) != len(want) {
		t.Fatalf("listing = %+v, want %v", files, want)
	}
	for p, isDir := range want {
		if d, ok := got[p]; !ok || d != isDir {
			t.Errorf("%s: listed %v (dir %v), want dir %v", p, ok, d, isDir)
		}
	}
}

// A path is confined to the session's own directory.
func TestFileRequestsCannotEscapeTheSession(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	if err := os.WriteFile(filepath.Join(a.cfg.Workspace, "secret.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+s.ID+"/files/..%2fsecret.txt", nil))
	if w.Code == 200 {
		t.Fatalf("escaped the session directory: %s", w.Body.String())
	}
}

// listed returns a session as the session list reports it.
func listed(t *testing.T, a *App, id string) Session {
	t.Helper()
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions", nil))
	var all []Session
	if err := json.Unmarshal(w.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %s is not in the list", id)
	return Session{}
}

// A session reports what it occupies on disk — its transcript and its working
// directory — so it is clear what deleting one frees.
func TestSessionReportsWhatItHoldsOnDisk(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	before := listed(t, a, s.ID)
	if before.DiskBytes <= 0 {
		t.Fatalf("a session with a transcript reports %d bytes", before.DiskBytes)
	}
	if w := upload(t, a, s.ID, "big.txt", strings.Repeat("x", 20000)); w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	after := listed(t, a, s.ID)
	if after.DiskBytes < before.DiskBytes+20000 {
		t.Errorf("after a 20 kB upload the session reports %d bytes, was %d", after.DiskBytes, before.DiskBytes)
	}
}

// declaredUpload states a size in the request without sending it, which is how
// the gateway learns a file is too large before any of it arrives.
func declaredUpload(t *testing.T, a *App, sessionID string, bytes int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/sessions/"+sessionID+"/files", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	req.ContentLength = bytes
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	return w
}

// An upload is refused above 250 MB, and refused on what the request declares,
// so a phone is told before it spends minutes sending one.
func TestUploadsAreRefusedAboveTheLimit(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)

	w := declaredUpload(t, a, s.ID, maxUpload+uploadEnvelope+1)
	if w.Code != 413 {
		t.Fatalf("an upload past the limit got status %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "250 MB") {
		t.Errorf("the refusal reads %q and does not say the limit", w.Body.String())
	}
	if w := declaredUpload(t, a, s.ID, 200<<20); w.Code == 413 {
		t.Errorf("a 200 MB upload was refused as too large: %s", w.Body.String())
	}
}

// The interface refuses a file on its own size, while the request that carries
// it adds a boundary and part headers around it. A file of exactly the limit
// must not be refused for that envelope, or the limit the screen states is one
// the operator cannot reach.
func TestAFileOfExactlyTheLimitIsNotRefusedForItsEnvelope(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	if w := declaredUpload(t, a, s.ID, maxUpload+512); w.Code == 413 {
		t.Errorf("a file of exactly the limit was refused: %s", w.Body.String())
	}
}

// An upload never replaces what is already there. A phone names every photo
// from its camera image.jpg, so two photos attached to one message used to be
// one photo, the second, under a message naming it twice; and a file a tool
// had written was lost to an upload that happened to share its name. A name
// already taken gets a number, and the answer says where the file landed.
func TestAnUploadNeverReplacesAFileAlreadyThere(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	landed := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		if w.Code != 201 {
			t.Fatalf("upload: status %d (%s)", w.Code, w.Body.String())
		}
		var out struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Path
	}
	first := landed(upload(t, a, s.ID, "image.jpg", "FIRST PHOTO"))
	second := landed(upload(t, a, s.ID, "image.jpg", "SECOND PHOTO"))
	third := landed(upload(t, a, s.ID, "image.jpg", "THIRD PHOTO"))
	if first != "image.jpg" || second != "image-2.jpg" || third != "image-3.jpg" {
		t.Errorf("uploads of one name landed at %q, %q, %q; want image.jpg, image-2.jpg, image-3.jpg", first, second, third)
	}
	for path, want := range map[string]string{first: "FIRST PHOTO", second: "SECOND PHOTO", third: "THIRD PHOTO"} {
		if got := callAPI(t, a, "GET", "/sessions/"+s.ID+"/files/"+path, nil).Body.String(); got != want {
			t.Errorf("%s holds %q, want %q", path, got, want)
		}
	}

	// What a tool wrote is no different from what was uploaded before.
	if err := os.WriteFile(filepath.Join(a.sessionWorkspace(s.ID), "notes"), []byte("THE AGENT'S NOTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := landed(upload(t, a, s.ID, "notes", "an upload")); got != "notes-2" {
		t.Errorf("an upload over a tool's file landed at %q, want notes-2", got)
	}
	if got := callAPI(t, a, "GET", "/sessions/"+s.ID+"/files/notes", nil).Body.String(); got != "THE AGENT'S NOTES" {
		t.Errorf("the tool's file now holds %q", got)
	}
}

// Attachments upload side by side, so two of one name can arrive together.
// Each still gets a name of its own.
func TestUploadsOfOneNameAtOnceEachKeepTheirOwnFile(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	const n = 8
	paths := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := upload(t, a, s.ID, "image.jpg", fmt.Sprintf("PHOTO %d", i))
			var out struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			paths <- out.Path
		}(i)
	}
	wg.Wait()
	close(paths)
	seen := map[string]bool{}
	for p := range paths {
		if seen[p] {
			t.Errorf("two uploads landed at %q", p)
		}
		seen[p] = true
	}
	if len(seen) != n {
		t.Errorf("%d uploads landed at %d names", n, len(seen))
	}
}

// A tool may make a symbolic link in its own directory to anywhere at all:
// the sandbox confines what the tool itself opens, not where a link it writes
// points. The files API is served by the agent, which no sandbox confines, so
// it follows no link out of the session's directory. A link that stays inside
// is an ordinary file; deleting a link removes the link, never what it names.
func TestFileRequestsFollowNoLinkOutOfTheSession(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	dir := a.ensureWorkspace(s.ID)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("OPERATOR SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("NOTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"out": secret, "outdir": outside, "inner": "notes.txt"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	for _, p := range []string{"out", "outdir/secret.txt"} {
		w := callAPI(t, a, "GET", "/sessions/"+s.ID+"/files/"+p, nil)
		if w.Code == 200 || strings.Contains(w.Body.String(), "OPERATOR SECRET") {
			t.Errorf("GET %s followed a link out of the session: %d %s", p, w.Code, w.Body.String())
		}
	}
	if w := callAPI(t, a, "GET", "/sessions/"+s.ID+"/files/inner", nil); w.Code != 200 || w.Body.String() != "NOTES" {
		t.Errorf("GET a link inside the session: %d %q, want 200 \"NOTES\"", w.Code, w.Body.String())
	}

	if w := callAPI(t, a, "DELETE", "/sessions/"+s.ID+"/files/outdir/secret.txt", nil); w.Code == 204 {
		t.Error("DELETE followed a link out of the session")
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("a file outside the session was deleted: %v", err)
	}
	if w := callAPI(t, a, "DELETE", "/sessions/"+s.ID+"/files/out", nil); w.Code != 204 {
		t.Errorf("DELETE a link: status %d, want 204 (%s)", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(dir, "out")); !os.IsNotExist(err) {
		t.Error("deleting a link left the link")
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "OPERATOR SECRET" {
		t.Errorf("deleting a link touched what it named: %q %v", b, err)
	}

	// An upload over a link's name writes a file of its own beside it.
	if err := os.Symlink(secret, filepath.Join(dir, "report.pdf")); err != nil {
		t.Fatal(err)
	}
	if w := upload(t, a, s.ID, "report.pdf", "UPLOADED"); w.Code != 201 {
		t.Fatalf("upload: status %d (%s)", w.Code, w.Body.String())
	}
	if b, _ := os.ReadFile(secret); string(b) != "OPERATOR SECRET" {
		t.Errorf("an upload wrote through a link, leaving %q outside the session", b)
	}
}
