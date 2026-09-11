package main

import "testing"

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
