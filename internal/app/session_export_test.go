package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

// exportSessionZip asks the gateway for a session's archive and returns the
// bytes it served.
func exportSessionZip(t *testing.T, a *App, sessionID string) []byte {
	t.Helper()
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+sessionID+"/export", nil))
	if w.Code != 200 {
		t.Fatalf("export status = %d: %s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, sessionID) {
		t.Errorf("Content-Disposition = %q, want the session's name in it", cd)
	}
	return w.Body.Bytes()
}

func importSessionZip(t *testing.T, a *App, blob []byte) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", "session.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(blob); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/sessions/import", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	return w
}

// seedSession builds a session with a transcript, a job, and a file, so an
// export has all of a session's parts to carry.
func seedSession(t *testing.T, a *App) *Session {
	t.Helper()
	s := newSession(t, a)
	s.Title = "Roof repair"
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "the roof leaks"})
	if _, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "1h", Prompt: "chase the roofer"}); err != nil {
		t.Fatal(err)
	}
	if w := upload(t, a, s.ID, "quote.txt", "1200 euro"); w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	return s
}

func zipNames(t *testing.T, blob []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(b)
	}
	return out
}

// An export carries everything the session owns: its metadata, its transcript,
// its jobs, and its working directory.
func TestExportCarriesEveryPartOfASession(t *testing.T) {
	a := newTestApp(t)
	s := seedSession(t, a)
	files := zipNames(t, exportSessionZip(t, a, s.ID))

	for _, name := range []string{"meta.json", "transcript.jsonl", "jobs.json", "job_runs.json", "files/quote.txt"} {
		if _, ok := files[name]; !ok {
			t.Errorf("export is missing %s", name)
		}
	}
	if files["files/quote.txt"] != "1200 euro" {
		t.Errorf("exported file = %q", files["files/quote.txt"])
	}
	var meta Session
	if err := json.Unmarshal([]byte(files["meta.json"]), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.ID != s.ID || meta.Title != "Roof repair" {
		t.Errorf("exported metadata = %+v", meta)
	}
	if !strings.Contains(files["transcript.jsonl"], "the roof leaks") {
		t.Errorf("transcript = %q", files["transcript.jsonl"])
	}
	var jobs []Job
	if err := json.Unmarshal([]byte(files["jobs.json"]), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Prompt != "chase the roofer" {
		t.Errorf("exported jobs = %+v", jobs)
	}
}

// An import puts the session back as it was, in whichever agent reads the
// archive: same identifier, same transcript, same jobs, same files.
func TestImportRestoresASessionWhole(t *testing.T) {
	source := newTestApp(t)
	s := seedSession(t, source)
	blob := exportSessionZip(t, source, s.ID)

	dest := newTestApp(t)
	catTool(t, dest, "quote.txt")
	w := importSessionZip(t, dest, blob)
	if w.Code != 201 {
		t.Fatalf("import status = %d: %s", w.Code, w.Body.String())
	}
	var got Session
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != s.ID || got.Title != "Roof repair" {
		t.Fatalf("imported session = %+v", got)
	}

	w = httptest.NewRecorder()
	dest.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+s.ID+"/transcript", nil))
	var entries []Entry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(source.store.Entries(s.ID)) {
		t.Errorf("imported %d entries, want %d", len(entries), len(source.store.Entries(s.ID)))
	}

	w = httptest.NewRecorder()
	dest.routes().ServeHTTP(w, httptest.NewRequest("GET", "/jobs?session_id="+s.ID, nil))
	var jobs []Job
	if err := json.Unmarshal(w.Body.Bytes(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Prompt != "chase the roofer" {
		t.Errorf("imported jobs = %+v", jobs)
	}

	if res := callTool(t, dest, s.ID, "catter"); res.Content != "1200 euro" {
		t.Errorf("imported working directory holds %q", res.Content)
	}

	// The transcript is searchable in the agent that imported it.
	w = httptest.NewRecorder()
	dest.routes().ServeHTTP(w, httptest.NewRequest("GET", "/search?q=roof", nil))
	var hits []SearchHit
	if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("imported transcript is not searchable")
	}
}

// Restoring over a session that is still present would make one identifier
// address two conversations, so it is refused rather than merged.
func TestImportRefusesAnIdentifierAlreadyPresent(t *testing.T) {
	a := newTestApp(t)
	s := seedSession(t, a)
	blob := exportSessionZip(t, a, s.ID)
	w := importSessionZip(t, a, blob)
	if w.Code != 409 {
		t.Fatalf("import over a live session = %d, want 409 (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), s.ID) {
		t.Errorf("refusal does not name the session: %s", w.Body.String())
	}
}

func TestImportRejectsSomethingThatIsNotASessionArchive(t *testing.T) {
	a := newTestApp(t)
	if w := importSessionZip(t, a, []byte("not a zip")); w.Code != 400 {
		t.Errorf("import of rubbish = %d, want 400", w.Code)
	}
}
