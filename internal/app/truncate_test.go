package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A title is the first line of a message cut to sixty bytes, and a cut is as
// likely to fall inside ü as beside it. Half a character is not text: stored,
// it came back as a replacement mark at the end of the title.
func TestACutNeverSplitsACharacter(t *testing.T) {
	said := "Bitte prüfe die Stückliste für das Gerät im Büro und erklär die größten Lücken"
	for n := 1; n < len(said); n++ {
		got := truncate(said, n)
		if !utf8.ValidString(got) {
			t.Fatalf("cut at %d left half a character: %q", n, got)
		}
		if len(strings.TrimSuffix(got, "…")) > n {
			t.Fatalf("cut at %d kept %d bytes", n, len(got))
		}
	}
	if title := firstLine(said); !utf8.ValidString(title) || !strings.HasSuffix(title, "…") {
		t.Errorf("title = %q, want whole characters and the mark of a cut", title)
	}
	if got := truncate("kurz", 60); got != "kurz" {
		t.Errorf("a short text was changed: %q", got)
	}
}
