package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The interface is served from disk with no version in its URLs, so a browser
// that heuristically caches app.js runs yesterday's script against today's
// markup. That failed silently — the transcript rendered and then emptied — and
// cost a debugging session before a hard reload explained it. Every asset must
// be revalidated.
func TestInterfaceAssetsAreRevalidated(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("// hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.cfg.WebDir = dir

	for _, path := range []string{"/app.js", "/"} {
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", path, w.Code)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q, want %q", path, got, "no-cache")
		}
	}
}

// no-cache is revalidation, not refusal to cache: an unchanged asset must still
// come back as a 304 rather than being sent again.
func TestAnUnchangedAssetStillRevalidatesTo304(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("// hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.cfg.WebDir = dir

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/app.js", nil))
	mod := w.Header().Get("Last-Modified")
	if mod == "" {
		t.Fatal("no Last-Modified on the first response")
	}

	r := httptest.NewRequest("GET", "/app.js", nil)
	r.Header.Set("If-Modified-Since", mod)
	w2 := httptest.NewRecorder()
	a.routes().ServeHTTP(w2, r)
	if w2.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", w2.Code)
	}
}
