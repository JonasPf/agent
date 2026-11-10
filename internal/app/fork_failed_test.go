package app

import (
	"encoding/json"
	"testing"
)

// A fork that fails part way had already been created: it stayed in the list
// as an empty "New session", with a directory and nothing else, for a request
// that had answered with an error.
func TestAForkThatFailsLeavesNoConversationBehind(t *testing.T) {
	r := newFakeRelease(t, hello)
	a := packagesApp(t, r)
	origin := newSession(t, a)
	// A package the store does not hold and no release offers: the fork
	// cannot keep it, and is refused.
	if _, err := a.store.UpdateSession(origin.ID, func(x *Session) {
		x.Packages = []Package{{Name: "gone", Paths: []string{"/nix/store/00000000000000000000000000000000-gone-1.0"}}}
	}); err != nil {
		t.Fatal(err)
	}
	before := len(a.store.Sessions())

	w := callAPI(t, a, "POST", "/sessions/"+origin.ID+"/fork", map[string]any{})
	if w.Code < 400 {
		t.Fatalf("fork = %d %s; it should have been refused, or the test shows nothing", w.Code, w.Body.String())
	}
	if got := len(a.store.Sessions()); got != before {
		t.Errorf("a refused fork left %d conversations behind", got-before)
	}
	for _, e := range a.store.Entries(origin.ID) {
		if e.EventKind == "fork" {
			t.Errorf("the origin records a fork that was not made: %q", e.Text)
		}
	}
}

// Every other answer that is a job carries what it costs; the answer to an
// edit did not, so the one moment a schedule changes was the one the price of
// the new schedule was missing.
func TestAnEditedJobAnswersWithWhatItNowCosts(t *testing.T) {
	a := newTestApp(t)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "20m", Prompt: "Check the oven."})
	if err != nil {
		t.Fatal(err)
	}
	w := callAPI(t, a, "PATCH", "/jobs/"+j.ID, map[string]string{"schedule": "6h"})
	if w.Code != 200 {
		t.Fatalf("PATCH = %d %s", w.Code, w.Body.String())
	}
	var got Job
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Schedule != "6h" {
		t.Errorf("schedule = %q, want 6h", got.Schedule)
	}
	if got.Estimate == nil {
		t.Fatalf("the edited job carries no estimate: %s", w.Body.String())
	}
	if got.Estimate.WakesPerDay != 4 {
		t.Errorf("wakes a day = %v, want the 4 of the new schedule", got.Estimate.WakesPerDay)
	}
}
