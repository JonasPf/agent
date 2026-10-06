package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The session list is read by every page whenever any count moves, and it
// reports two figures that are costly to measure: the context each conversation
// would send, and what it holds on disk. They are kept between reads rather than
// measured afresh for every session on every read, so these say that what the
// list reports still follows what the conversation does.

func TestTheListCountsContextAsTheConversationGrows(t *testing.T) {
	a, _ := modelBackedApp(t, strings.Repeat("A long answer. ", 200))
	s := newSession(t, a)
	before := listed(t, a, s.ID)
	sendMessage(t, a, s.ID, "Tell me everything.")
	after := listed(t, a, s.ID)
	if after.ContextUsed <= before.ContextUsed+500 {
		t.Errorf("context used went from %d to %d across a long answer", before.ContextUsed, after.ContextUsed)
	}
	if after.EntryCount <= before.EntryCount {
		t.Errorf("entry count went from %d to %d across a turn", before.EntryCount, after.EntryCount)
	}
}

// A tool writes into the conversation's directory, and the list says so on its
// next read, not a minute later.
func TestTheListSeesWhatAToolWrote(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	writeTool(t, dir, "scribble",
		"#!/bin/sh\nhead -c 30000 /dev/zero > blob.bin\nprintf '{\"ok\":true,\"content\":\"written\"}'\n")
	a.tools.dir = dir
	if _, f := a.tools.Load(a); len(f) > 0 {
		t.Fatalf("load failures: %v", f)
	}
	srv := toolCallingModel(t, "scribble", 1, "Written.")
	t.Cleanup(srv.Close)
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s := newSession(t, a)
	before := listed(t, a, s.ID)
	sendMessage(t, a, s.ID, "Write the blob.")
	after := listed(t, a, s.ID)
	if after.DiskBytes < before.DiskBytes+30000 {
		t.Errorf("after a tool wrote 30 kB the list reports %d bytes, was %d", after.DiskBytes, before.DiskBytes)
	}
}

// Deleting a file frees its room at once, which is what the figure is for.
func TestTheListSeesWhatADeleteFreed(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	if w := upload(t, a, s.ID, "big.txt", strings.Repeat("x", 40000)); w.Code != 201 {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	before := listed(t, a, s.ID)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("DELETE", "/sessions/"+s.ID+"/files/big.txt", nil))
	if w.Code >= 300 {
		t.Fatalf("delete status = %d: %s", w.Code, w.Body.String())
	}
	after := listed(t, a, s.ID)
	if after.DiskBytes > before.DiskBytes-40000 {
		t.Errorf("after deleting 40 kB the list reports %d bytes, was %d", after.DiskBytes, before.DiskBytes)
	}
}

// The list carries how many jobs each conversation holds.
func TestTheListCountsEachConversationsJobs(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	other := newSession(t, a)
	for _, p := range []string{"one", "two"} {
		if _, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "1h", Prompt: p}); err != nil {
			t.Fatal(err)
		}
	}
	if got := listed(t, a, s.ID).JobCount; got != 2 {
		t.Errorf("job count = %d, want 2", got)
	}
	if got := listed(t, a, other.ID).JobCount; got != 0 {
		t.Errorf("a conversation with no jobs lists %d", got)
	}
}
