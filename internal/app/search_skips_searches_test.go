package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// boilerSearched is a conversation that said something and then searched for
// it, as the agent does. The word is in what was said, and in the search's call
// and what the search returned, whose numbers come back too.
func boilerSearched(t *testing.T, dir string) (*App, *Session, Entry, map[int]bool) {
	t.Helper()
	srv := scriptedModel(t,
		toolStep{tool: "session_search", args: `{"query":"boiler"}`},
		toolStep{text: "The boiler service is on Tuesday."})
	a := metricsApp(t, dir, srv)
	s := newSession(t, a)
	said := a.append(s.ID, Entry{Type: "message", Role: "assistant", Text: "The boiler service is booked for Tuesday."})
	say(t, a, s.ID, "When is the boiler service?")
	searches := map[int]bool{}
	var found bool
	for _, e := range a.store.Entries(s.ID) {
		for _, tc := range e.ToolCalls {
			if tc.Name == "session_search" {
				searches[e.Seq] = true
			}
		}
		if e.Role == "tool" && e.ToolName == "session_search" {
			searches[e.Seq] = true
			found = found || strings.Contains(string(e.ToolResult), "boiler")
		}
	}
	if !found {
		t.Fatal("the turn holds no search that found the boiler; the script did not run as meant")
	}
	return a, s, said, searches
}

// hitSeq is the entry number a hit line of the search tool names.
func hitSeq(t *testing.T, s *Session, line string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(strings.TrimPrefix(line, s.ID+" #"), "%d", &n); err != nil {
		t.Fatalf("no entry number in %q", line)
	}
	return n
}

// hitsFor is what the search tool, run as shipped, answers in a session.
func hitsFor(t *testing.T, a *App, s *Session, query string) []string {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"query": query})
	res := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "session_search", raw)
	if !res.OK {
		t.Fatalf("session_search failed: %s%s", res.Content, res.Error)
	}
	var hits []string
	for _, line := range strings.Split(res.Content, "\n") {
		if strings.HasPrefix(line, s.ID+" #") {
			hits = append(hits, line)
		}
	}
	return hits
}

// "A search's own call and what it returned are not in the index, so a later
// search finds what the conversation said, not the searches for it." Every
// search was indexed like any other tool call, so the next search for the same
// words found the last one first: in a conversation of 723 searches, at least
// 154 answered with an earlier search, and one found itself.
func TestASearchDoesNotFindTheSearchesBeforeIt(t *testing.T) {
	a, s, said, searches := boilerSearched(t, t.TempDir())

	hits := hitsFor(t, a, s, "boiler")
	var saidFound bool
	for _, h := range hits {
		n := hitSeq(t, s, h)
		saidFound = saidFound || n == said.Seq
		if searches[n] {
			t.Errorf("a search for boiler found the earlier search for it: %s", h)
		}
	}
	if !saidFound {
		t.Errorf("a search for boiler did not find #%d, where it was said:\n%s", said.Seq, strings.Join(hits, "\n"))
	}

	// The search screen reads the same index.
	w := callAPI(t, a, "GET", "/search?q=boiler&session="+s.ID, nil)
	var found []SearchHit
	if err := json.Unmarshal(w.Body.Bytes(), &found); err != nil {
		t.Fatalf("search: %v (%s)", err, w.Body.String())
	}
	for _, h := range found {
		if searches[h.Seq] {
			t.Errorf("the search screen found the agent's search for boiler: #%d %q", h.Seq, h.Snippet)
		}
	}
}

// "An index holding what the agent no longer indexes is rebuilt from the
// transcripts at startup." A conversation's earlier searches were already in
// the index when the agent stopped indexing them, and an index is otherwise
// rebuilt only when it is empty.
func TestAnIndexFromBeforeSearchesWereLeftOutIsRebuilt(t *testing.T) {
	dir := t.TempDir()
	a, s, said, searches := boilerSearched(t, dir)
	db := a.store.DB()
	// What the earlier agent wrote: every entry's calls and results, searches
	// included, and nothing to say which rules wrote it.
	for _, e := range a.store.Entries(s.ID) {
		body := e.Text
		for _, tc := range e.ToolCalls {
			body += "\n" + tc.Name + " " + tc.Arguments
		}
		body += "\n" + string(e.ToolResult)
		if _, err := db.Exec(`insert into entry_fts(session_id, seq, body) values(?,?,?)`, s.ID, e.Seq, body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`drop table search_index`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	b := metricsApp(t, dir, nil)
	hits, err := b.store.Search("boiler", s.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var saidFound bool
	for _, h := range hits {
		saidFound = saidFound || h.Seq == said.Seq
		if searches[h.Seq] {
			t.Errorf("after a restart a search for boiler still finds the earlier search: #%d %q", h.Seq, h.Snippet)
		}
	}
	if !saidFound {
		t.Errorf("after a restart a search for boiler does not find #%d, where it was said: %+v", said.Seq, hits)
	}
}
