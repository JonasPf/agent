package app

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+pred.ID+"/rotate",
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
