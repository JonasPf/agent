package app

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// get asks for a path the way a browser does, accepting gzip or not.
func get(t *testing.T, a *App, path string, gz bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	if gz {
		r.Header.Set("Accept-Encoding", "gzip, deflate, br")
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}

func gunzip(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A transcript is JSON carrying every tool result, and the page asks for it on a
// phone. JSON and the interface's own scripts shrink five to ten times
// compressed, so whatever the browser accepts compressed is sent compressed.
func TestWhatABrowserAcceptsCompressedIsSentCompressed(t *testing.T) {
	a := newTestApp(t)
	web := t.TempDir()
	script := "'use strict';\n" + strings.Repeat("function f() { return 1; }\n", 200)
	if err := os.WriteFile(filepath.Join(web, "app.js"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	a.cfg.WebDir = web
	s := newSession(t, a)
	a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: strings.Repeat("A long answer. ", 500)})

	w := get(t, a, "/sessions/"+s.ID+"/transcript", true)
	var entries []Entry
	if err := json.Unmarshal([]byte(gunzip(t, w)), &entries); err != nil || len(entries) == 0 {
		t.Fatalf("the compressed transcript does not read back: %v", err)
	}
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Errorf("a compressed answer does not vary on Accept-Encoding: %q", w.Header().Get("Vary"))
	}

	w = get(t, a, "/app.js", true)
	if body := gunzip(t, w); body != script {
		t.Errorf("the compressed script reads back as %d bytes, want %d", len(body), len(script))
	}

	// A browser that does not say it accepts gzip is sent the plain bytes.
	w = get(t, a, "/sessions/"+s.ID+"/transcript", false)
	if w.Header().Get("Content-Encoding") != "" || !json.Valid(w.Body.Bytes()) {
		t.Errorf("a client that accepts nothing compressed was sent %q", w.Header().Get("Content-Encoding"))
	}
}

// A file the operator downloads goes out as it is stored: it is often already
// compressed, and a byte range of it has to mean the file's own bytes.
func TestAFileDownloadIsSentAsItIs(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	content := strings.Repeat("plain text in a file\n", 200)
	if w := upload(t, a, s.ID, "notes.txt", content); w.Code != 201 {
		t.Fatalf("upload = %d", w.Code)
	}
	w := get(t, a, "/sessions/"+s.ID+"/files/notes.txt", true)
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("a download was sent with Content-Encoding %q", got)
	}
	if w.Body.String() != content {
		t.Errorf("the download came back as %d bytes, want %d", w.Body.Len(), len(content))
	}
}
