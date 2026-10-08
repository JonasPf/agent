package app

import (
	"fmt"
	"testing"
)

// A fork copies a whole conversation. Copied one entry at a time, each entry
// was its own write to the transcript, its own commit to the search index, its
// own rewrite of the metadata, and its own event to every page — a page that
// then announced copied replies as though they were new. The copy is now one
// write, and pages hear that the list moved, not every entry it moved by.
func TestAForkCopiesTheConversationInOneGo(t *testing.T) {
	a := newTestApp(t)
	s := newSession(t, a)
	for i := 0; i < 40; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		a.append(s.ID, Entry{Type: "message", Role: role, Text: fmt.Sprintf("message %d about the greenhouse", i)})
	}
	events := a.hub.add()
	defer a.hub.remove(events)

	fork, err := a.Fork(s, s.SessionConfig)
	if err != nil {
		t.Fatal(err)
	}

	copiedEvents := 0
	for {
		select {
		case e := <-events:
			if e.Kind == "entry" && e.SessionID == fork.ID && e.Entry != nil && e.Entry.CarriedFrom == s.ID &&
				e.Entry.EventKind == "" {
				copiedEvents++
			}
			continue
		default:
		}
		break
	}
	if copiedEvents != 0 {
		t.Errorf("pages were sent %d copied entries one by one", copiedEvents)
	}

	origin := a.store.Entries(s.ID)
	copied := a.store.Entries(fork.ID)
	var want []string
	for _, e := range origin {
		if e.Type == "message" {
			want = append(want, e.Text)
		}
	}
	var got []string
	for i, e := range copied {
		if e.Seq != i {
			t.Fatalf("fork entry %d has seq %d", i, e.Seq)
		}
		if e.Type == "message" {
			got = append(got, e.Text)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the fork holds %d messages, want the origin's %d in order", len(got), len(want))
	}
	hits, err := a.store.Search("greenhouse", fork.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 40 {
		t.Errorf("search finds %d of the fork's 40 copied messages", len(hits))
	}
	if got := unreadOf(t, a, fork.ID); got != 0 {
		t.Errorf("a fork starts with %d unread", got)
	}
}
