package app

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A container has to be able to say whether the process inside it is answering.
// Doing that with curl means a network client in the runtime image, kept there
// for a request the agent could make of itself — so the agent makes it. The
// image ships the one binary it was always going to ship.

// liveAgent is an agent whose /status can answer: the scheduler because the
// endpoint reports the breaker, and a model gateway that is a local stub rather
// than OpenRouter, because a health check must not depend on the public web.
func liveAgent(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	a.sched = NewScheduler(a)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no key in a test", 401)
	}))
	t.Cleanup(gateway.Close)
	a.or = NewOpenRouter("")
	a.or.base = gateway.URL
	return a
}

// listening starts a server on a loopback port and returns the address in the
// form the agent is configured with.
func listening(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	return ":" + port
}

func TestHealthPassesAgainstARunningAgent(t *testing.T) {
	addr := listening(t, liveAgent(t).routes())
	if err := Health(addr); err != nil {
		t.Errorf("Health(%s) = %v, want nil against a live agent", addr, err)
	}
}

// The check exists to notice a process that is up and not answering, so it has
// to fail on a bad answer rather than on a closed socket alone.
func TestHealthFailsOnAnErrorFromTheAgent(t *testing.T) {
	addr := listening(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "database is gone", 500)
	}))
	err := Health(addr)
	if err == nil {
		t.Fatal("Health passed a 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want it to name the status", err)
	}
}

func TestHealthFailsWhenNothingIsListening(t *testing.T) {
	// A port nothing is bound to: taken, then released before the check runs.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := fmt.Sprintf(":%d", l.Addr().(*net.TCPAddr).Port)
	l.Close()

	if err := Health(addr); err == nil {
		t.Error("Health passed with nothing listening")
	}
}

// The address the agent was configured with is a listen address, which may name
// no host at all. The check has to reach the loopback either way.
func TestHealthAcceptsTheAddressFormsTheAgentIsConfiguredWith(t *testing.T) {
	addr := listening(t, liveAgent(t).routes())
	port := strings.TrimPrefix(addr, ":")
	for _, form := range []string{":" + port, "0.0.0.0:" + port, "127.0.0.1:" + port} {
		if err := Health(form); err != nil {
			t.Errorf("Health(%q) = %v, want nil", form, err)
		}
	}
}
