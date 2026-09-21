package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// slowModel holds a call open until it is released, so a test can act on a
// session while it is working rather than after it has finished.
func slowModel(t *testing.T, release <-chan struct{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Second):
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"delta": map[string]any{"content": "finished after all"}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// A turn runs for as long as the work takes, so the operator needs a way to
// stop one. Stopping ends the turn where it is and says so in the transcript:
// a conversation that went quiet because it was stopped must not look like one
// that went quiet for no reason.
func TestARunningTurnCanBeStopped(t *testing.T) {
	a := newTestApp(t)
	release := make(chan struct{})
	srv := slowModel(t, release)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s := newSession(t, a)
	send := httptest.NewRequest("POST", "/sessions/"+s.ID+"/messages",
		strings.NewReader(`{"text":"Find every study there is."}`))
	send.Header.Set("Content-Type", "application/json")
	a.routes().ServeHTTP(httptest.NewRecorder(), send)

	deadline := time.Now().Add(2 * time.Second)
	for !a.Busy(s.ID) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !a.Busy(s.ID) {
		t.Fatal("the session never started working")
	}

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/cancel", nil))
	if w.Code != 200 {
		t.Fatalf("cancel status = %d: %s", w.Code, w.Body.String())
	}
	settle(t, a, s.ID)

	var stopped bool
	var texts []string
	for _, e := range a.store.Entries(s.ID) {
		if e.EventKind == "cancelled" {
			stopped = true
		}
		if e.Type == "message" && e.Role == "assistant" {
			texts = append(texts, e.Text)
		}
	}
	if !stopped {
		t.Error("stopping a turn left no entry saying so")
	}
	for _, text := range texts {
		if strings.Contains(text, "finished after all") {
			t.Error("the stopped turn went on to answer")
		}
	}
}

// Stopping a session that is not working is not an error the operator caused:
// the turn they meant to stop has already finished. It says so rather than
// pretending to have stopped something.
func TestStoppingAnIdleSessionSaysSo(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/cancel", nil))
	if w.Code != 409 {
		t.Fatalf("cancel of an idle session = %d, want 409 (%s)", w.Code, w.Body.String())
	}
	for _, e := range a.store.Entries(s.ID) {
		if e.EventKind == "cancelled" {
			t.Error("a session that was not working was written as stopped")
		}
	}
}
