package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// titleModel answers a conversation's first turn with a plain reply and its
// second call — the title — with whatever the test gives it. A turn that calls
// no tools makes one call, so the second request is the titler's, and the test
// keeps it to read what the titler was sent.
func titleModel(t *testing.T, title func(w http.ResponseWriter)) (*httptest.Server, func() ChatRequest) {
	t.Helper()
	var mu sync.Mutex
	var calls int
	var titled ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls++
		n := calls
		if n == 2 {
			_ = json.Unmarshal(body, &titled)
		}
		mu.Unlock()
		if n == 2 {
			title(w)
			return
		}
		streamText(w, "Done.")
	}))
	t.Cleanup(srv.Close)
	return srv, func() ChatRequest {
		mu.Lock()
		defer mu.Unlock()
		return titled
	}
}

func streamText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"content": text}}}})
	fmt.Fprintf(w, "data: %s\n\n", chunk)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// firstTurn sends a new conversation its first message over the API and waits
// for the turn, and the title that follows it, to finish.
func firstTurn(t *testing.T, srv *httptest.Server, text string) (*App, *Session) {
	t.Helper()
	a := newTestApp(t)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL
	s := newSession(t, a)
	body, _ := json.Marshal(map[string]string{"text": text})
	req := httptest.NewRequest("POST", "/sessions/"+s.ID+"/messages", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if w.Code >= 300 {
		t.Fatalf("send: status %d (%s)", w.Code, w.Body.String())
	}
	settle(t, a, s.ID)
	return a, s
}

const voiceoverRequest = "Make a voiceover for this clip\n\nAttached: clip.mov"

// "A title names the conversation and is never the model's answer to it": a
// first message that reads as a request reached the titler as one, and the
// model's refusal to carry it out became the conversation's name.
func TestATitleIsNeverTheModelsAnswerToTheMessage(t *testing.T) {
	srv, _ := titleModel(t, func(w http.ResponseWriter) {
		streamText(w, "I can't help with this request as stated. I'm not able to:\n- open attachments")
	})
	a, s := firstTurn(t, srv, voiceoverRequest)
	if got, want := sessionTitle(t, a, s.ID), "Make a voiceover for this clip"; got != want {
		t.Errorf("title = %q, want the message's first line %q", got, want)
	}
}

// The titler is told what to do by the system and handed the message as the
// thing to name, so the operator's words are never the instruction it follows.
func TestTheTitlerIsHandedTheMessageToName(t *testing.T) {
	srv, sent := titleModel(t, func(w http.ResponseWriter) { streamText(w, "Voiceover for a clip") })
	firstTurn(t, srv, voiceoverRequest)
	req := sent()
	if len(req.Messages) != 2 {
		t.Fatalf("the titler was sent %d messages, want an instruction and the message to name", len(req.Messages))
	}
	instruction, named := req.Messages[0], req.Messages[1]
	if instruction.Role != "system" {
		t.Errorf("the instruction travels as %q, want system", instruction.Role)
	}
	text, _ := named.Content.(string)
	if named.Role != "user" || !strings.Contains(text, voiceoverRequest) {
		t.Errorf("the message to name is %s %q, want a user message carrying %q", named.Role, text, voiceoverRequest)
	}
	if strings.Contains(text, "six words") {
		t.Errorf("the instruction is in the message to name: %q", text)
	}
}

// A title that fits is kept as the model wrote it.
func TestAShortTitleIsKept(t *testing.T) {
	srv, _ := titleModel(t, func(w http.ResponseWriter) { streamText(w, "Voiceover for a clip") })
	a, s := firstTurn(t, srv, voiceoverRequest)
	if got := sessionTitle(t, a, s.ID); got != "Voiceover for a clip" {
		t.Errorf("title = %q, want the model's %q", got, "Voiceover for a clip")
	}
}

// A title call that fails leaves the message's first line as the title, and
// leaves the agent running: the answer it failed to get is not read.
func TestAFailedTitleFallsBackToTheMessage(t *testing.T) {
	srv, _ := titleModel(t, func(w http.ResponseWriter) {
		http.Error(w, `{"error":{"message":"no such model"}}`, http.StatusBadRequest)
	})
	a, s := firstTurn(t, srv, voiceoverRequest)
	if got, want := sessionTitle(t, a, s.ID), "Make a voiceover for this clip"; got != want {
		t.Errorf("title = %q, want the message's first line %q", got, want)
	}
}
