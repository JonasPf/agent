package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordingModel is fakeModel that keeps every request body, so a test can read
// what the model was actually shown rather than what the transcript stored.
func recordingModel(t *testing.T, reply string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, string(body))
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": reply}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// The operator asks for a reminder, the job fires, and the model answers in the
// transcript — into a session nobody is looking at. The wake must therefore
// arrive marked as a wake: it is the only thing that tells the model the
// operator is not there to read a reply.
func TestTheModelIsToldAWakeCameFromAJob(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "Reminding you.")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "1h",
		Prompt: "Remind the user to feed the cat."})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: j.Prompt, JobID: j.ID}); err != nil {
		t.Fatal(err)
	}

	if len(*seen) == 0 {
		t.Fatal("the model was never called")
	}
	body := (*seen)[0]
	if !strings.Contains(body, j.ID) {
		t.Errorf("the model was not told which job woke it; request was %s", body)
	}
	if !strings.Contains(body, "not present") {
		t.Errorf("the model was not told the operator is absent; request was %s", body)
	}
	if !strings.Contains(body, "Remind the user to feed the cat.") {
		t.Errorf("the job's prompt did not reach the model; request was %s", body)
	}
}

// The same turn typed by the operator carries no marker: the agent is talking to
// someone who is reading, and dressing that up as a wake would have it interrupt
// a person already looking at the answer.
func TestATypedMessageReachesTheModelUnmarked(t *testing.T) {
	a := newTestApp(t)
	srv, seen := recordingModel(t, "Hello.")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: "hello"}); err != nil {
		t.Fatal(err)
	}
	if len(*seen) == 0 {
		t.Fatal("the model was never called")
	}
	if strings.Contains((*seen)[0], "not present") {
		t.Errorf("a typed message was marked as a job wake; request was %s", (*seen)[0])
	}
}

// flakyModel fails every attempt of the first turn — retries included — and
// records each request after that.
func flakyModel(t *testing.T, reply string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n++
		if n <= chatAttempts {
			http.Error(w, "upstream timed out", 504)
			return
		}
		seen = append(seen, string(body))
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": reply}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// A wake whose model call failed left its prompt in the transcript unanswered.
// The next wake appended the identical prompt, and the model — seeing the same
// instruction twice with nothing between — answered it twice. One reminder
// reached the operator as two.
func TestAFailedWakeDoesNotMakeTheNextOneAnswerTwice(t *testing.T) {
	a := newTestApp(t)
	srv, seen := flakyModel(t, "Reminder: time to stretch.")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	a.or.retryBase = 0 // the backoff is tested on its own; do not sit through it

	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m",
		Prompt: "Remind me to stretch."})
	if err != nil {
		t.Fatal(err)
	}

	// The first wake fails every attempt, exactly as the timed-out one did.
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: j.Prompt, JobID: j.ID}); err == nil {
		t.Fatal("the first wake was supposed to fail")
	}
	a.appendEvent(s.ID, Entry{EventKind: "job_error", JobID: j.ID,
		Text: "job run failed: upstream timed out"})

	// The second wake succeeds.
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: j.Prompt, JobID: j.ID}); err != nil {
		t.Fatal(err)
	}

	if len(*seen) == 0 {
		t.Fatal("the model was never called a second time")
	}
	body := (*seen)[0]
	if !strings.Contains(body, "produced no reply") {
		t.Errorf("the model was not told the first wake failed; request was %s", body)
	}
	if strings.Count(body, "Remind me to stretch.") < 2 {
		t.Fatalf("expected both wakes in the request; got %s", body)
	}
	// Both prompts are there, but they are no longer adjacent: the failure sits
	// between them, which is what stops the model answering the pair.
	first := strings.Index(body, "Remind me to stretch.")
	fail := strings.Index(body, "produced no reply")
	last := strings.LastIndex(body, "Remind me to stretch.")
	if !(first < fail && fail < last) {
		t.Errorf("the failure is not between the two wakes; request was %s", body)
	}
}
