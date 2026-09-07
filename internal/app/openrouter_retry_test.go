package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// sse writes one streamed completion in OpenRouter's shape.
func sse(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	chunk, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"content": text}}},
	})
	fmt.Fprintf(w, "data: %s\n\n", chunk)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// retryClient is an OpenRouter pointed at srv, with the wait between attempts
// removed so a test does not sit through the backoff.
func retryClient(srv *httptest.Server) *OpenRouter {
	o := NewOpenRouter("test-key")
	o.base = srv.URL
	o.retryBase = 0
	return o
}

// A job run that failed because the upstream was briefly unavailable used to
// lose the whole wake: one attempt, one error, one reminder that never arrived.
func TestATransientFailureIsRetried(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			http.Error(w, "upstream unavailable", 503)
			return
		}
		sse(w, "Reminding you.")
	}))
	defer srv.Close()

	res, err := retryClient(srv).Chat(context.Background(), ChatRequest{Model: "m"}, nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if res.Text != "Reminding you." {
		t.Errorf("text = %q", res.Text)
	}
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Errorf("made %d attempts, want 2", got)
	}
}

func TestARateLimitIsRetried(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", 429)
			return
		}
		sse(w, "ok")
	}))
	defer srv.Close()

	if _, err := retryClient(srv).Chat(context.Background(), ChatRequest{Model: "m"}, nil); err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Errorf("made %d attempts, want 2", got)
	}
}

// An exhausted account, a withdrawn model, a bad key: retrying spends time and
// money to be told the same thing, and delays the breaker that should trip.
func TestAPermanentFailureIsNotRetried(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		http.Error(w, "insufficient credit", 402)
	}))
	defer srv.Close()

	_, err := retryClient(srv).Chat(context.Background(), ChatRequest{Model: "m"}, nil)
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Fatalf("error = %v, want a PermanentError", err)
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Errorf("made %d attempts, want 1", got)
	}
}

// A malformed request fails the same way every time.
func TestABadRequestIsNotRetried(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		http.Error(w, "bad request", 400)
	}))
	defer srv.Close()

	if _, err := retryClient(srv).Chat(context.Background(), ChatRequest{Model: "m"}, nil); err == nil {
		t.Fatal("want an error")
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Errorf("made %d attempts, want 1", got)
	}
}

// Once the answer has begun arriving it has already been shown in the browser
// and counted. Starting again would say the first half twice.
func TestAFailureAfterTheAnswerBeganIsNotRetried(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": "half an ans"}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		w.(http.Flusher).Flush()
		// The stream reports its own failure and stops, mid-answer.
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"upstream died\"}}\n\n")
	}))
	defer srv.Close()

	var seen strings.Builder
	_, err := retryClient(srv).Chat(context.Background(), ChatRequest{Model: "m"},
		func(d string) { seen.WriteString(d) })
	if err == nil {
		t.Fatal("want an error")
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Errorf("made %d attempts, want 1 — the answer had already started", got)
	}
	if seen.String() != "half an ans" {
		t.Errorf("streamed %q, want the partial answer exactly once", seen.String())
	}
}

// Three attempts and no more: a job that cannot reach the model must fail and
// let the scheduler's backoff and breaker take over.
func TestRetriesAreBounded(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		http.Error(w, "upstream unavailable", 503)
	}))
	defer srv.Close()

	_, err := retryClient(srv).Chat(context.Background(), ChatRequest{Model: "m"}, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %v, want it to carry the last failure", err)
	}
	if got := atomic.LoadInt32(&n); got != int32(chatAttempts) {
		t.Errorf("made %d attempts, want %d", got, chatAttempts)
	}
}

// A cancelled turn stops at once rather than working through its retries.
func TestACancelledCallIsNotRetried(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		http.Error(w, "upstream unavailable", 503)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := retryClient(srv).Chat(ctx, ChatRequest{Model: "m"}, nil); err == nil {
		t.Fatal("want an error")
	}
	if got := atomic.LoadInt32(&n); got > 1 {
		t.Errorf("made %d attempts on a cancelled context", got)
	}
}
