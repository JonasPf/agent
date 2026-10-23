package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func transcriptPath(dir, id string) string {
	return filepath.Join(dir, "sessions", id, "transcript.jsonl")
}

// served is the transcript as the API serves it.
func served(t *testing.T, a *App, id string) []Entry {
	t.Helper()
	w := get(t, a, "/sessions/"+id+"/transcript", false)
	if w.Code != 200 {
		t.Fatalf("GET transcript = %d: %s", w.Code, w.Body.String())
	}
	var out []Entry
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func note(a *App, id, text string) Entry {
	return a.append(id, Entry{Type: "message", Role: "user", Text: text})
}

// The host's disk filled while an entry was being written, and the write
// stopped part way through a line. The next entry was written straight after
// it, on the same line, so the file lost both: the half that was cut and the
// whole one glued to it. Then, read back after a restart, the conversation was
// two entries short, and the next two it wrote reused numbers it already had.
//
// A write now begins on a line of its own whatever the last one left, and a
// conversation numbers what it writes after the highest number it holds.
func TestAnEntryCutShortTakesNothingElseWithIt(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	s := newSession(t, a)
	note(a, s.ID, "first")
	note(a, s.ID, "second")
	cut := note(a, s.ID, "the one the full disk cut short")

	// What the disk left: the last line, without its end.
	path := transcriptPath(dir, s.ID)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b[:len(b)-20], 0o644); err != nil {
		t.Fatal(err)
	}
	after := note(a, s.ID, "written once there was room again")

	again := newTestAppAt(t, dir)
	var found bool
	seen := map[int]bool{}
	for _, e := range served(t, again, s.ID) {
		if e.Text == "written once there was room again" {
			found = true
		}
		seen[e.Seq] = true
	}
	if !found {
		t.Fatal("the entry written after the cut one was lost with it")
	}
	if seen[cut.Seq] {
		t.Errorf("entry %d was cut short on disk, and still read back", cut.Seq)
	}

	next := note(again, s.ID, "after the restart")
	if next.Seq <= after.Seq {
		t.Errorf("after the restart the next entry is %d, a number already used (the last was %d)",
			next.Seq, after.Seq)
	}
}

// A write that fails leaves nothing behind: not the entry in memory, where the
// conversation would go on as though it were stored, and not a number that a
// restart would then hand out again.
func TestAWriteThatFailsLeavesNoTrace(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	s := newSession(t, a)
	note(a, s.ID, "stored")
	before := len(a.store.Entries(s.ID))

	// A directory where the file should be refuses every write, root's too.
	path := transcriptPath(dir, s.ID)
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".kept"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	failed := note(a, s.ID, "this one cannot be written")
	if got := len(a.store.Entries(s.ID)); got != before {
		t.Errorf("a write that failed is still held: %d entries, want %d", got, before)
	}
	for _, e := range served(t, a, s.ID) {
		if e.Text == "this one cannot be written" {
			t.Error("the API serves an entry that was never stored")
		}
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, kept, 0o644); err != nil {
		t.Fatal(err)
	}
	next := note(a, s.ID, "stored after all")
	if next.Seq != failed.Seq {
		t.Errorf("the next entry is %d; the failed write's %d should have been free again", next.Seq, failed.Seq)
	}
}

// A line that cannot be read leaves a gap in the numbers, and an entry's number
// is then no longer its position. Reverting to a message after the gap looked
// it up by position, found a different entry, and refused.
func TestARevertPastAnUnreadableLineFindsItsMessage(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	s := newSession(t, a)
	note(a, s.ID, "before the damage")
	note(a, s.ID, "the line the disk ruined")
	note(a, s.ID, "after the damage")

	path := transcriptPath(dir, s.ID)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ruined := []byte(`"the line the disk ruined"`)
	var out []byte
	for _, line := range splitLines(b) {
		if containsBytes(line, ruined) {
			line = line[:len(line)/2]
		}
		out = append(append(out, line...), '\n')
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}

	again := newTestAppAt(t, dir)
	if w := revertTo(t, again, s.ID, seqOf(t, again, s.ID, "after the damage")); w.Code != 200 {
		t.Errorf("revert past an unreadable line = %d: %s", w.Code, w.Body.String())
	}
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

func containsBytes(b, sub []byte) bool {
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == string(sub) {
			return true
		}
	}
	return false
}
