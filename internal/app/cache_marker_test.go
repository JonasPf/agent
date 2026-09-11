package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func hasMarker(m ChatMessage) bool {
	b, _ := json.Marshal(m)
	return strings.Contains(string(b), "cache_control")
}

// One marker, at the end of the message list. Everything above it is read at a
// fraction of price; only what the last call added is paid for in full.
func TestCacheMarkerGoesOnTheLastMessage(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	got := withCacheMarker(msgs, "5m")
	if hasMarker(got[0]) {
		t.Error("the system message carries a marker; it should be on the tail")
	}
	if hasMarker(got[1]) {
		t.Error("a middle message carries a marker")
	}
	if !hasMarker(got[len(got)-1]) {
		t.Error("the last message carries no marker")
	}
}

// The idle gate: no expected next call means no write, so a cache nobody reads
// is never paid for.
func TestNoMarkerWhenNoTTL(t *testing.T) {
	msgs := []ChatMessage{{Role: "system", Content: "p"}, {Role: "user", Content: "a"}}
	for _, m := range withCacheMarker(msgs, "") {
		if hasMarker(m) {
			t.Fatal("a marker was written with no TTL")
		}
	}
}

func TestMarkerOnAnEmptyListIsHarmless(t *testing.T) {
	if got := withCacheMarker(nil, "5m"); len(got) != 0 {
		t.Fatalf("got %d messages from nil; want 0", len(got))
	}
}

// A marker must not damage the message it is attached to.
func TestMarkerPreservesContent(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Content: "the actual text"}}
	got := withCacheMarker(msgs, "5m")
	b, _ := json.Marshal(got[0])
	if !strings.Contains(string(b), "the actual text") {
		t.Fatalf("content lost: %s", b)
	}
}

// A tool result carries its body in Content too, and must survive marking.
func TestMarkerOnAToolResult(t *testing.T) {
	msgs := []ChatMessage{{Role: "tool", ToolCallID: "c1", Name: "read", Content: "file body"}}
	got := withCacheMarker(msgs, "5m")
	b, _ := json.Marshal(got[0])
	if !strings.Contains(string(b), "file body") || !strings.Contains(string(b), "c1") {
		t.Fatalf("tool result damaged: %s", b)
	}
}
