package main

import (
	"encoding/json"
	"os"
	"testing"
)

// A command may be allowed up to maxTimeout, and no more: asked for longer, it
// is held to the most, so an overrun is still the tool's to report. Unasked, it
// gets the default.
func TestAllowedHoldsATimeoutBetweenTheDefaultAndTheMost(t *testing.T) {
	for _, c := range []struct{ asked, want int }{
		{0, 120}, {-5, 120}, {30, 30}, {600, 600}, {900, 900}, {901, 900}, {86400, 900},
	} {
		if got := Allowed(c.asked); got != c.want {
			t.Errorf("Allowed(%d) = %d, want %d", c.asked, got, c.want)
		}
	}
}

// Waiting for something in one call is what saves a turn from asking again and
// again whether it is done, so the most has to be long enough to be worth it —
// and the registry's limit has to sit above it, or the longest wait a caller
// may ask for ends in a kill with nothing said.
func TestTheRegistryLimitSitsAboveTheLongestWait(t *testing.T) {
	if maxTimeout < 900 {
		t.Errorf("maxTimeout = %ds; a command must be able to wait fifteen minutes", maxTimeout)
	}
	b, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Agent struct {
			Timeout int `json:"timeout_seconds"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Agent.Timeout <= maxTimeout {
		t.Errorf("the registry kills bash at %ds, at or below the %ds a command may ask for", m.Agent.Timeout, maxTimeout)
	}
}

func TestClipLeavesShortOutputAlone(t *testing.T) {
	body, cut := Clip("hello", 20)
	if body != "hello" || cut != 0 {
		t.Fatalf("Clip(short) = %q, %d; want %q, 0", body, cut, "hello")
	}
}

func TestClipAtExactlyTheCapDoesNotAnnounce(t *testing.T) {
	body, cut := Clip("abcde", 5)
	if body != "abcde" || cut != 0 {
		t.Fatalf("Clip(exact) = %q, %d; want %q, 0", body, cut, "abcde")
	}
}

// The notice is the point: a model reading a cut body must not take it for the
// whole answer. So the dropped count comes back and the caller says it.
func TestClipReportsWhatItDropped(t *testing.T) {
	body, cut := Clip("abcdefghij", 4)
	if body != "abcd" {
		t.Fatalf("Clip kept %q; want %q", body, "abcd")
	}
	if cut != 6 {
		t.Fatalf("Clip dropped %d; want 6", cut)
	}
}

func TestClipZeroCapKeepsNothingAndSaysSo(t *testing.T) {
	body, cut := Clip("abc", 0)
	if body != "" || cut != 3 {
		t.Fatalf("Clip(0) = %q, %d; want %q, 3", body, cut, "")
	}
}
