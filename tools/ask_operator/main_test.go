package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckNamesWhatIsWrong(t *testing.T) {
	one := []question{{Name: "a", Type: "text"}}
	for want, a := range map[string]ask{
		"message is required": {Questions: one},
		"one of the two":      {Message: "m"},
		"has no name":         {Message: "m", Questions: []question{{Type: "text"}}},
		"has type":            {Message: "m", Questions: []question{{Name: "a", Type: "slider"}}},
		"with no options":     {Message: "m", Questions: []question{{Name: "a", Type: "choices"}}},
		"two questions":       {Message: "m", Questions: []question{{Name: "a", Type: "text"}, {Name: "a", Type: "text"}}},
		"must be an .html":    {Message: "m", Page: "notes.txt"},
	} {
		err := check(a, "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v, want %q", a, err, want)
		}
	}
	if err := check(ask{Message: "m", Questions: one}, ""); err != nil {
		t.Errorf("a good form was refused: %v", err)
	}
}

func TestAPageOverTheLimitIsRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.html")
	if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, maxPage+1); err != nil {
		t.Fatal(err)
	}
	if err := check(ask{Message: "m", Page: "big.html"}, p); err == nil || !strings.Contains(err.Error(), "MB") {
		t.Errorf("got %v", err)
	}
}

func TestAnAskIDSortsByTimeAndCannotClimbOut(t *testing.T) {
	a := newID(time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	b := newID(time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC))
	if a >= b || !validID.MatchString(a) {
		t.Errorf("%q, %q", a, b)
	}
	if _, err := saveBeside(t.TempDir(), "../../etc", "x", "y"); err == nil {
		t.Error("an id that climbs out was accepted")
	}
}

func TestAnAskWithoutAPageSavesNothing(t *testing.T) {
	root := t.TempDir()
	if err := record(root, "a1", ask{Message: "m", Questions: []question{{Name: "a", Type: "text"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := saveBeside(root, "a1", "x.json", "y"); err == nil {
		t.Error("a form saved a file")
	}
}
