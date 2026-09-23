package app

import (
	"context"
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

// The offer to stop appears the moment the conversation says it is working, and
// it has to hold from that moment. The turn used to register its own stop once
// the queue reached it, which left a window — short on a laptop, reliable in the
// container — where the interface showed a stop that answered "this conversation
// is not working on anything" while the turn went on to run.
func TestATurnCanBeStoppedBeforeItStarts(t *testing.T) {
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
	sw := httptest.NewRecorder()
	a.routes().ServeHTTP(sw, send)
	if sw.Code != 202 {
		t.Fatalf("send = %d: %s", sw.Code, sw.Body.String())
	}
	if !a.Busy(s.ID) {
		t.Fatal("the conversation does not say it is working the moment the message is queued")
	}

	// No waiting for the turn to start. The message has been accepted, the
	// interface is already showing a stop, and pressing it must work.
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/cancel", nil))
	if w.Code != 200 {
		t.Fatalf("stopping a queued turn = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	settle(t, a, s.ID)

	var stopped bool
	for _, e := range a.store.Entries(s.ID) {
		if e.EventKind == "cancelled" {
			stopped = true
		}
		if e.Type == "message" && e.Role == "assistant" && strings.Contains(e.Text, "finished after all") {
			t.Error("the stopped turn went on to answer")
		}
	}
	if !stopped {
		t.Error("stopping a queued turn left no entry saying so")
	}
}

// A turn queued behind another is work the operator is waiting on, so it is work
// they can stop. Stopping takes the turns in the order they were queued: the one
// running first, then the one behind it.
func TestEachQueuedTurnIsStoppedInTurn(t *testing.T) {
	a := newTestApp(t)
	release := make(chan struct{})
	srv := slowModel(t, release)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s := newSession(t, a)
	for _, text := range []string{"First question.", "Second question."} {
		req := httptest.NewRequest("POST", "/sessions/"+s.ID+"/messages",
			strings.NewReader(`{"text":"`+text+`"}`))
		req.Header.Set("Content-Type", "application/json")
		a.routes().ServeHTTP(httptest.NewRecorder(), req)
	}
	for i := 1; i <= 2; i++ {
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/cancel", nil))
		if w.Code != 200 {
			t.Fatalf("stop %d of 2 = %d, want 200 (%s)", i, w.Code, w.Body.String())
		}
	}
	settle(t, a, s.ID)

	stopped := 0
	for _, e := range a.store.Entries(s.ID) {
		if e.EventKind == "cancelled" {
			stopped++
		}
		if e.Type == "message" && e.Role == "assistant" && strings.Contains(e.Text, "finished after all") {
			t.Error("a stopped turn went on to answer")
		}
	}
	if stopped != 2 {
		t.Errorf("%d turns were written as stopped, want 2", stopped)
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/cancel", nil))
	if w.Code != 409 {
		t.Errorf("stopping a session with nothing left = %d, want 409", w.Code)
	}
}

// A scheduled wake runs as the session's turn, so it stops the same way. What it
// must not do is count as a failure: three failures in an hour open the breaker
// and stop every job in the agent, and an operator stopping three wakes is not
// the gateway being broken.
func TestAStoppedWakeIsStoppedRatherThanFailed(t *testing.T) {
	a := newTestApp(t)
	release := make(chan struct{})
	srv := slowModel(t, release)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	a.sched = NewScheduler(a)

	s := newSession(t, a)
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Has anything changed?", AfterActing: afterContinue})
	if err != nil {
		t.Fatal(err)
	}
	j.NextRunAt = time.Now().Add(-time.Second)
	if err := a.store.PutJob(j); err != nil {
		t.Fatal(err)
	}
	a.sched.tick(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for !a.Busy(s.ID) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !a.Busy(s.ID) {
		t.Fatal("the wake never started")
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/cancel", nil))
	if w.Code != 200 {
		t.Fatalf("stopping a wake = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	settle(t, a, s.ID)

	runs := runsOf(t, a, j.ID)
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Outcome == jobFailed {
		t.Errorf("a stopped wake is logged as a failure: %q", runs[0].Message)
	}
	if !strings.Contains(strings.ToLower(runs[0].Message), "stopped") {
		t.Errorf("the run log does not say it was stopped: %q", runs[0].Message)
	}
	live, err := a.store.Job(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.FailCount != 0 {
		t.Errorf("a stopped wake counted %d failures against the job", live.FailCount)
	}
	if a.sched.State().Open {
		t.Error("stopping a wake opened the breaker")
	}
	var stopped bool
	for _, e := range a.store.Entries(s.ID) {
		if e.EventKind == "cancelled" {
			stopped = true
		}
	}
	if !stopped {
		t.Error("a stopped wake left no entry saying so in the conversation")
	}
}
