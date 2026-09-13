package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// Scoped search is what makes compaction safe: what a fold takes out of the
// context it leaves in the index, reachable from the session that folded it.
func TestSearchScopedToOneSession(t *testing.T) {
	a := newTestApp(t)
	one, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	two, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	a.append(one.ID, Entry{Type: "message", Role: "user", Text: "the chimney needs repointing"})
	a.append(two.ID, Entry{Type: "message", Role: "user", Text: "the chimney is fine"})

	scoped, err := a.store.Search("chimney", one.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].SessionID != one.ID {
		t.Fatalf("scoped search returned %d hits from %+v; want 1 from %s", len(scoped), scoped, one.ID)
	}

	all, err := a.store.Search("chimney", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("unscoped search returned %d hits; want 2", len(all))
	}
}

// Through the HTTP surface the tool actually uses.
func TestSearchScopeOverHTTP(t *testing.T) {
	a := newTestApp(t)
	one, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	two, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	a.append(one.ID, Entry{Type: "message", Role: "user", Text: "gutters need clearing"})
	a.append(two.ID, Entry{Type: "message", Role: "user", Text: "gutters are clear"})

	get := func(q string) []SearchHit {
		t.Helper()
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/search?"+q, nil))
		if w.Code != 200 {
			t.Fatalf("GET /search?%s = %d: %s", q, w.Code, w.Body)
		}
		var hits []SearchHit
		if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil {
			t.Fatal(err)
		}
		return hits
	}
	if got := get("q=gutters&session=" + one.ID); len(got) != 1 {
		t.Fatalf("scoped: got %d hits; want 1", len(got))
	}
	if got := get("q=gutters"); len(got) != 2 {
		t.Fatalf("unscoped: got %d hits; want 2", len(got))
	}
}
