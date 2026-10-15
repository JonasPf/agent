package app

import (
	"fmt"
	"sync"
	"testing"
)

// A session is changed from many sides at once: a turn counting its cost and
// moving its last activity, the operator renaming it and moving where it
// compacts, a page reading it through, every page listing it. Each of them
// used to change the one shared copy in place while the others read it — a
// data race the race detector reports here — and a title written by the model
// after its call could land over a rename made during it. Every change now
// makes a new copy under the store's lock, so each reader holds a session that
// nothing changes under it, and no change is lost, in memory or on disk.
func TestASessionChangedFromManySidesAtOnceKeepsEveryChange(t *testing.T) {
	a := newTestApp(t)
	a.or = NewOpenRouter("test-key")
	a.or.base = usageModel(t, 1000, 0).URL
	s := newSession(t, a)

	const turns, renames = 4, 40
	var wg sync.WaitGroup
	for i := 0; i < turns; i++ {
		if w := callAPI(t, a, "POST", "/sessions/"+s.ID+"/messages", map[string]any{"text": fmt.Sprintf("Turn %d.", i)}); w.Code != 202 {
			t.Fatalf("send: status %d (%s)", w.Code, w.Body.String())
		}
	}
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < renames; i++ {
			callAPI(t, a, "PATCH", "/sessions/"+s.ID, map[string]any{"title": fmt.Sprintf("Title %d", i)})
			callAPI(t, a, "PATCH", "/sessions/"+s.ID, map[string]any{"compact_at_tokens": 50000 + i})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < renames; i++ {
			callAPI(t, a, "GET", "/sessions", nil)
			callAPI(t, a, "GET", "/sessions/"+s.ID, nil)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < renames; i++ {
			callAPI(t, a, "POST", "/sessions/"+s.ID+"/read", map[string]any{})
		}
	}()
	wg.Wait()
	settle(t, a, s.ID)

	got := a.store.Session(s.ID)
	want := fmt.Sprintf("Title %d", renames-1)
	if got.Title != want {
		t.Errorf("title = %q, want the operator's last rename %q", got.Title, want)
	}
	if got.CompactAtTokens != 50000+renames-1 {
		t.Errorf("compact_at_tokens = %d, want the last one set, %d", got.CompactAtTokens, 50000+renames-1)
	}
	if got.Cost < float64(turns)*0.001-1e-9 {
		t.Errorf("cost = %v, want at least one call's cost for each of %d turns", got.Cost, turns)
	}
	disk := newTestAppAt(t, a.cfg.DataDir).store.Session(s.ID)
	if disk.Title != got.Title || disk.Cost != got.Cost || disk.CompactAtTokens != got.CompactAtTokens {
		t.Errorf("on disk: %q %v %d; in memory: %q %v %d",
			disk.Title, disk.Cost, disk.CompactAtTokens, got.Title, got.Cost, got.CompactAtTokens)
	}
}
