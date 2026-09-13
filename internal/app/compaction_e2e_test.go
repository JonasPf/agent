package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// growWithToolResults makes a session large the way a real one gets large: the
// bulk lands in tool results, where the model's own words are a sentence between
// fetches. Growth counts the projected messages, so such a session must still
// compact — counting entry text alone would let it grow without bound.
func growWithToolResults(t *testing.T, a *App, s *Session, rounds int) {
	t.Helper()
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "Find broadband options."})
	for i := 0; i < rounds; i++ {
		a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "Fetching.",
			ToolCalls: []ToolCall{{ID: "c", Name: "web_fetch", Arguments: `{"url":"https://x"}`}}})
		body, _ := json.Marshal(map[string]any{"ok": true,
			"content": strings.Repeat("page text ", 500)})
		a.append(s.ID, Entry{Type: "message", Role: "tool", ToolCallID: "c",
			ToolName: "web_fetch", ToolResult: body})
		a.append(s.ID, Entry{Type: "message", Role: "user", Text: "Keep going."})
	}
}

// The criterion: passing the threshold compacts in place. No new session, and
// the identifier does not change.
func TestPassingTheThresholdCompactsInPlace(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- broadband: fibre only")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	s.CompactAtTokens = 3000
	s.KeepVerbatimTokens = 500
	if err := a.store.PutSession(s); err != nil {
		t.Fatal(err)
	}
	growWithToolResults(t, a, s, 6)

	before := projectedTokens(a.store.Entries(s.ID))
	if before < s.CompactAtTokens {
		t.Fatalf("setup grew only %d tokens, need %d", before, s.CompactAtTokens)
	}
	sessionsBefore := len(a.store.Sessions())

	if err := a.runTurn(context.Background(), s, turnOpts{UserText: "Anything else?"}); err != nil {
		t.Fatal(err)
	}

	if got := len(a.store.Sessions()); got != sessionsBefore {
		t.Fatalf("compaction created a session: %d → %d", sessionsBefore, got)
	}
	entries := a.store.Entries(s.ID)
	if newestCompaction(entries) < 0 {
		t.Fatal("passing the threshold wrote no compaction entry")
	}
	if after := projectedTokens(entries); after >= before {
		t.Fatalf("compaction did not shrink what is sent: %d → %d", before, after)
	}
}

// Nothing is deleted. A compaction appends; the covered entries stay on disk and
// stay findable, which is what makes this compression rather than loss.
func TestCompactionDeletesNothingAndStaysSearchable(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- summary")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	s.CompactAtTokens = 3000
	s.KeepVerbatimTokens = 500
	_ = a.store.PutSession(s)
	a.append(s.ID, Entry{Type: "message", Role: "user", Text: "the chimney needs repointing"})
	growWithToolResults(t, a, s, 6)
	countBefore := len(a.store.Entries(s.ID))

	if err := a.Compact(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	entries := a.store.Entries(s.ID)
	if len(entries) != countBefore+2 {
		t.Fatalf("entry count %d → %d; want exactly two appended (prompt, compaction)",
			countBefore, len(entries))
	}
	found := false
	for _, e := range entries {
		if strings.Contains(e.Text, "chimney needs repointing") {
			found = true
		}
	}
	if !found {
		t.Fatal("a covered entry was removed from the transcript")
	}

	// And it is still findable, through the surface the tool uses.
	hits, err := a.store.Search("repointing", s.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("a covered entry is no longer returned by search")
	}

	// It is gone from what the model is sent, which is the whole point.
	for _, m := range Project(entries) {
		if s, ok := m.Content.(string); ok && strings.Contains(s, "chimney needs repointing") {
			t.Fatal("a covered entry still reaches the model")
		}
	}
}

// A compaction refreshes memory, which is the only way a memory written during
// a long conversation ever reaches it.
func TestCompactionRefreshesMemory(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- summary")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	a.cfg.MemoryCapacity = 8000

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	s.CompactAtTokens = 3000
	s.KeepVerbatimTokens = 500
	_ = a.store.PutSession(s)
	growWithToolResults(t, a, s, 6)

	if _, err := a.AddMemory("the operator prefers all times in UTC", s.ID); err != nil {
		t.Fatal(err)
	}
	// Before compacting, the prompt in force cannot know about it.
	if p, _ := a.promptEntry(s.ID); strings.Contains(systemMessage(p.Sections), "UTC") {
		t.Fatal("memory reached the prompt without a compaction")
	}

	if err := a.Compact(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	p, ok := a.promptEntry(s.ID)
	if !ok {
		t.Fatal("no prompt entry after compacting")
	}
	if !strings.Contains(systemMessage(p.Sections), "UTC") {
		t.Fatal("the compaction did not refresh memory into the prompt")
	}
}

// Compaction can be asked for, which is how a memory written a moment ago takes
// effect without waiting for the threshold. Driven over HTTP.
func TestCompactionCanBeRequested(t *testing.T) {
	a := newTestApp(t)
	srv, _ := recordingModel(t, "## Decisions\n- asked for")
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	s.CompactAtTokens = 1 << 20 // far above what this test builds
	s.KeepVerbatimTokens = 500
	_ = a.store.PutSession(s)
	growWithToolResults(t, a, s, 6)

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("POST", "/sessions/"+s.ID+"/compact", nil))
	if w.Code != 200 {
		t.Fatalf("POST /compact = %d: %s", w.Code, w.Body)
	}
	if newestCompaction(a.store.Entries(s.ID)) < 0 {
		t.Fatal("a requested compaction wrote nothing")
	}
}

// A failed compaction leaves the conversation running rather than failing a turn.
func TestAFailedCompactionLeavesTheConversationRunning(t *testing.T) {
	a := newTestApp(t)
	// A summariser that restates rather than summarises: the guard must reject it.
	srv, _ := recordingModel(t, strings.Repeat("restating everything at length ", 4000))
	a.or = NewOpenRouter("test-key")
	a.or.base = srv.URL

	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	s.CompactAtTokens = 3000
	s.KeepVerbatimTokens = 500
	_ = a.store.PutSession(s)
	growWithToolResults(t, a, s, 6)

	if err := a.Compact(context.Background(), s); err == nil {
		t.Fatal("a summary larger than what it covers was accepted")
	}
	if newestCompaction(a.store.Entries(s.ID)) >= 0 {
		t.Fatal("a rejected summary was written anyway")
	}
	// The turn that follows still runs.
	if err := a.runTurn(context.Background(), s, turnOpts{UserText: "still there?"}); err != nil {
		t.Fatalf("the conversation stopped after a failed compaction: %v", err)
	}
}

// A fork copies everything and leaves the origin alone.
func TestForkCopiesTheWholeConversationAndLeavesTheOriginActive(t *testing.T) {
	a := newTestApp(t)
	origin, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	a.append(origin.ID, Entry{Type: "message", Role: "user", Text: "the first thing said"})
	a.append(origin.ID, Entry{Type: "message", Role: "assistant", Text: "the first answer"})

	fork, err := a.Fork(origin, SessionConfig{Model: "other/model"})
	if err != nil {
		t.Fatal(err)
	}
	if fork.ID == origin.ID {
		t.Fatal("a fork reused its origin's identifier")
	}
	if fork.ForkedFrom != origin.ID {
		t.Fatalf("fork records %q as its origin; want %q", fork.ForkedFrom, origin.ID)
	}

	var copied int
	for _, e := range a.store.Entries(fork.ID) {
		if e.Type == "message" {
			copied++
		}
	}
	if copied != 2 {
		t.Fatalf("fork carries %d messages; want the whole conversation, 2", copied)
	}
	// Exactly one prompt entry, and it is the fork's own.
	prompts := 0
	for _, e := range a.store.Entries(fork.ID) {
		if e.Type == "prompt" {
			prompts++
		}
	}
	if prompts != 1 {
		t.Fatalf("fork has %d prompt entries; want 1, its own", prompts)
	}
	if p, _ := a.promptEntry(fork.ID); !strings.Contains(systemMessage(p.Sections), "other/model") {
		t.Fatal("the fork's prompt was not written under its own configuration")
	}

	reloaded := a.store.Session(origin.ID)
	if reloaded.Status != "active" {
		t.Fatalf("the origin was archived by being forked: %q", reloaded.Status)
	}
}
