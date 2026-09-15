package app

import (
	"context"
	"testing"
)

// A check is a shell command the model wrote through the schedule tool, run on
// every wake. It is a bash call in all but name, so it gets what a bash call in
// the same conversation gets: what a program needs to start, and that
// conversation's grants. It used to inherit the agent's whole environment,
// model key included, and with the web open to it a check could send it away.

func checkMet(t *testing.T, a *App, s *Session, check string) bool {
	t.Helper()
	j, err := a.CreateJob(JobSpec{SessionID: s.ID, Schedule: "2m", Check: check, Prompt: "tell me"})
	if err != nil {
		t.Fatal(err)
	}
	met, _, err := NewScheduler(a).runCheck(context.Background(), j, s)
	if err != nil {
		t.Fatal(err)
	}
	return met
}

func TestACheckSeesWhatABashCallInItsConversationWould(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-should-not-be-visible")
	t.Setenv("GH_TOKEN", "ghp_granted")
	t.Setenv("UNGRANTED_SECRET", "not-for-checks")
	a := newTestApp(t)
	granted, err := a.NewSession(SessionConfig{Model: "test/model", GrantedEnv: []string{"GH_TOKEN"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}

	if !checkMet(t, a, granted, `[ -n "$PATH" ]`) {
		t.Error("a check has no PATH; it cannot run anything")
	}
	if !checkMet(t, a, granted, `[ -z "${OPENROUTER_API_KEY+set}" ]`) {
		t.Error("a check can read the model key")
	}
	if !checkMet(t, a, granted, `[ -z "${UNGRANTED_SECRET+set}" ]`) {
		t.Error("a check can read a variable nobody granted")
	}
	if !checkMet(t, a, granted, `[ "$GH_TOKEN" = ghp_granted ]`) {
		t.Error("a check in a conversation granted GH_TOKEN cannot read it")
	}
	if !checkMet(t, a, plain, `[ -z "${GH_TOKEN+set}" ]`) {
		t.Error("a check in a conversation granted nothing can read GH_TOKEN")
	}
}
