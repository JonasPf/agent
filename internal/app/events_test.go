package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func eventKinds(a *App, sessionID string) []string {
	var out []string
	for _, e := range a.store.Entries(sessionID) {
		if e.Type == "event" {
			out = append(out, e.EventKind)
		}
	}
	return out
}

// An event exists only where nothing else records what happened. An action whose
// result is already in the transcript, or already a row on another screen, is not
// written a second time.
func TestCreatingAJobWritesNoEvent(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "1h",
		Prompt: "check on it"}); err != nil {
		t.Fatal(err)
	}
	if got := eventKinds(a, s.ID); len(got) != 0 {
		t.Errorf("creating a job wrote %v; the job row is the record", got)
	}
}

func TestRegisteringAToolWritesNoEvent(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTool(t, dir, "latecomer", "#!/bin/sh\nprintf '{\"ok\":true,\"content\":\"hi\"}'\n")
	a.tools.dir = dir
	if _, f := a.ReloadTools(s.ID); len(f) > 0 {
		t.Fatalf("load failures: %v", f)
	}
	if got := eventKinds(a, s.ID); len(got) != 0 {
		t.Errorf("registering a tool wrote %v; the reload result is the record", got)
	}
}

func TestUploadWritesNoEvent(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := upload(t, a, s.ID, "notes.txt", "hello upload"); w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	if got := eventKinds(a, s.ID); len(got) != 0 {
		t.Errorf("an upload wrote %v; the file is the record", got)
	}
}

// A check that exits non-zero costs no model call and produces no message, so
// the event is the only thing separating "polled for hours, found nothing" from
// "died on the first tick".
func TestACheckThatDoesNotFireStillLeavesALine(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "2m",
		Check: "exit 1", Prompt: "tell me"})
	if err != nil {
		t.Fatal(err)
	}
	sched := NewScheduler(a)
	met, _, err := sched.runCheck(context.Background(), j, s)
	if err != nil {
		t.Fatal(err)
	}
	if met {
		t.Fatal("exit 1 means the condition is not met")
	}
	got := eventKinds(a, s.ID)
	if len(got) != 1 || got[0] != "job_check" {
		t.Fatalf("events = %v, want exactly one job_check", got)
	}
}

// The prompt is not an event. It is the conversation's opening entry and has its
// own type, so nothing that reasons about events has to make an exception for it.
func TestPromptIsItsOwnEntryType(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	entries := a.store.Entries(s.ID)
	if len(entries) != 1 {
		t.Fatalf("want one entry at creation, got %d", len(entries))
	}
	if entries[0].Type != "prompt" {
		t.Errorf("prompt entry type = %q, want \"prompt\"", entries[0].Type)
	}
	if got := eventKinds(a, s.ID); len(got) != 0 {
		t.Errorf("the prompt must not be an event, got %v", got)
	}
	if _, ok := a.promptEntry(s.ID); !ok {
		t.Error("promptEntry must still find it")
	}
	// It is still kept out of the model's messages.
	for _, m := range Project(entries) {
		if strings.Contains(toString(m.Content), "You are a single-user autonomous agent") {
			t.Error("the prompt entry must never be projected into a model request")
		}
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

// The transcript API must keep returning the prompt so the interface can render
// position zero.
func TestTranscriptStillCarriesThePrompt(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/"+s.ID+"/transcript", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"type":"prompt"`) {
		t.Errorf("transcript must carry the prompt entry: %s", truncate(w.Body.String(), 200))
	}
}
