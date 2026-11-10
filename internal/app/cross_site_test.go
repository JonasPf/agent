package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// post sends a POST the way a browser would, with the headers a browser adds
// by itself and a page cannot set.
func post(t *testing.T, url, body string, headers map[string]string) int {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	// What a form on another site can send without the browser asking first.
	req.Header.Set("Content-Type", "text/plain")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// The proxy's password is the whole of the access control, and a browser that
// holds it sends it with every request to the agent — one a page on another
// site makes included. Such a page could start a conversation and send it a
// message, which runs a shell. The browser says where a request came from, and
// one from another site is refused before it does anything.
func TestARequestFromAnotherSiteChangesNothing(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	before := len(a.store.Sessions())

	for name, headers := range map[string]map[string]string{
		"a browser that says so":          {"Sec-Fetch-Site": "cross-site", "Origin": "https://elsewhere.example"},
		"a sibling host":                  {"Sec-Fetch-Site": "same-site", "Origin": "https://other.example"},
		"an older browser, by its origin": {"Origin": "https://elsewhere.example"},
	} {
		if code := post(t, srv.URL+"/sessions", `{}`, headers); code != 403 {
			t.Errorf("%s: POST /sessions = %d, want 403", name, code)
		}
		if code := post(t, srv.URL+"/sessions/"+s.ID+"/messages", `{"text":"run this"}`, headers); code != 403 {
			t.Errorf("%s: POST messages = %d, want 403", name, code)
		}
	}
	if got := len(a.store.Sessions()); got != before {
		t.Errorf("another site started %d conversations", got-before)
	}
	if a.Busy(s.ID) || len(a.store.Entries(s.ID)) != 1 {
		t.Errorf("another site's message reached the conversation: %+v", a.store.Entries(s.ID))
	}
}

// The interface's own requests, and those of a program that is not a browser
// at all — curl, the tasks in the Taskfile — go through as they always did.
func TestTheInterfaceAndPlainClientsAreNotRefused(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)

	for name, headers := range map[string]map[string]string{
		"the interface":                     {"Sec-Fetch-Site": "same-origin", "Origin": srv.URL},
		"the interface in an older browser": {"Origin": srv.URL},
		"a program that is not a browser":   {},
	} {
		if code := post(t, srv.URL+"/sessions", `{}`, headers); code != 201 {
			t.Errorf("%s: POST /sessions = %d, want 201", name, code)
		}
	}
}

// The socket carries every entry of every conversation as it is written. A
// page on another site may open a socket anywhere, and the browser sends the
// password with it, so the agent has to look at where the page is from.
func TestTheSocketOpensOnlyForTheInterface(t *testing.T) {
	a := newTestApp(t)
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	host := strings.TrimPrefix(srv.URL, "http://")

	dial := func(headers map[string]string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h := http.Header{}
		for k, v := range headers {
			h.Set(k, v)
		}
		c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
		if err == nil {
			c.CloseNow()
		}
		return err
	}

	for name, headers := range map[string]map[string]string{
		"another site":                 {"Origin": "https://elsewhere.example"},
		"another site that says so":    {"Origin": "https://elsewhere.example", "Sec-Fetch-Site": "cross-site"},
		"another port of the same box": {"Origin": "http://127.0.0.1:1"},
	} {
		if err := dial(headers); err == nil {
			t.Errorf("%s opened the socket", name)
		}
	}
	for name, headers := range map[string]map[string]string{
		"the interface":                   {"Origin": "http://" + host},
		"a program that is not a browser": {},
		// A proxy that sends its own name for the upstream as the host still
		// says which host the browser asked for.
		"the interface behind a proxy": {"Origin": "https://agent.example", "X-Forwarded-Host": "agent.example"},
	} {
		if err := dial(headers); err != nil {
			t.Errorf("%s was refused the socket: %v", name, err)
		}
	}
}
