package app

import (
	"fmt"
	"testing"
)

// Starting up reads every session the agent holds back off disk, and the search
// index too when it is empty. These say that a restart comes back with exactly
// what it had, however that reading is done.

func TestARestartedAgentHoldsEverySessionItHad(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	want := map[string]int{}
	for i := 0; i < 24; i++ {
		s := newSession(t, a)
		for k := 0; k <= i%5; k++ {
			a.append(s.ID, Entry{Type: "message", Role: "user", Text: fmt.Sprintf("message %d of session %d", k, i)})
		}
		want[s.ID] = len(a.store.Entries(s.ID))
	}
	a.store.DB().Close()

	b := newTestAppAt(t, dir)
	if got := len(b.store.Sessions()); got != len(want) {
		t.Fatalf("a restart holds %d sessions, want %d", got, len(want))
	}
	for id, n := range want {
		entries := b.store.Entries(id)
		if len(entries) != n {
			t.Errorf("session %s came back with %d entries, want %d", id, len(entries), n)
			continue
		}
		for i, e := range entries {
			if e.Seq != i {
				t.Errorf("session %s entry %d has seq %d", id, i, e.Seq)
				break
			}
		}
	}
}

// The index is derived from the transcripts, so an empty one — a database lost,
// or one from before the index — is rebuilt from them at startup.
func TestAnEmptySearchIndexIsRebuiltFromTheTranscripts(t *testing.T) {
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	var ids []string
	for i := 0; i < 6; i++ {
		s := newSession(t, a)
		a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: fmt.Sprintf("the greenhouse humidity reading %d", i)})
		ids = append(ids, s.ID)
	}
	if _, err := a.store.DB().Exec(`delete from entry_fts`); err != nil {
		t.Fatal(err)
	}
	a.store.DB().Close()

	b := newTestAppAt(t, dir)
	hits, err := b.store.Search("greenhouse", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, h := range hits {
		found[h.SessionID] = true
	}
	for _, id := range ids {
		if !found[id] {
			t.Errorf("session %s is not in the rebuilt index", id)
		}
	}
}
