package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// web_search reaches a search API, and the key it needs is the operator's, set
// once for the agent rather than granted to a conversation (ADR-059). These
// drive the tool as a subprocess with the API substituted — the one thing R5
// allows to be, alongside the model provider.

// call is what the substituted API saw. The tool runs as a subprocess, so the
// only evidence of what it sent is what the server was handed.
type apiCall struct {
	made   bool
	header http.Header
	body   string
}

// tavilyServer stands in for the search API and records the call.
func tavilyServer(t *testing.T, body string, status int) (string, *apiCall) {
	t.Helper()
	got := &apiCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.made, got.header, got.body = true, r.Header.Clone(), string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, got
}

const tavilyBody = `{"query":"landlock go","results":[
  {"title":"Landlock for Go","url":"https://pkg.go.dev/landlock","content":"Bindings for the kernel's Landlock LSM.","score":0.97},
  {"title":"Landlock: unprivileged sandboxing","url":"https://docs.kernel.org/userspace-api/landlock.html","content":"A process may restrict itself.","score":0.9}
]}`

func searchWith(t *testing.T, a *App, s *Session, args map[string]any) toolResult {
	t.Helper()
	raw, _ := json.Marshal(args)
	return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "web_search", raw)
}

func TestWebSearchReturnsWhatTheAPIAnswered(t *testing.T) {
	url, got := tavilyServer(t, tavilyBody, 200)
	t.Setenv("TAVILY_API_KEY", "tvly-test-key")
	t.Setenv("TAVILY_URL", url)
	a, s := userlandSession(t)
	allowServer(t, a, url)

	res := searchWith(t, a, s, map[string]any{"query": "landlock go", "limit": 5})
	if !res.OK {
		t.Fatalf("search failed: %s%s", res.Content, res.Error)
	}
	for _, want := range []string{"Landlock for Go", "https://pkg.go.dev/landlock",
		"Bindings for the kernel's Landlock LSM.", "docs.kernel.org"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("the answer does not carry %q:\n%s", want, res.Content)
		}
	}
	if !got.made {
		t.Fatal("the tool did not call the API")
	}
	if h := got.header.Get("Authorization"); h != "Bearer tvly-test-key" {
		t.Errorf("Authorization = %q, want the operator's key as a bearer token", h)
	}
	if !strings.Contains(got.body, `"landlock go"`) {
		t.Errorf("the query was not sent: %s", got.body)
	}
}

// The limit is the caller's, and it is what the API is asked for rather than
// something trimmed afterwards: a search that fetches ten to show three is
// three times the price for the same answer.
func TestWebSearchAsksTheAPIForTheLimitItWasGiven(t *testing.T) {
	url, got := tavilyServer(t, tavilyBody, 200)
	t.Setenv("TAVILY_API_KEY", "tvly-test-key")
	t.Setenv("TAVILY_URL", url)
	a, s := userlandSession(t)
	allowServer(t, a, url)

	if res := searchWith(t, a, s, map[string]any{"query": "landlock go", "limit": 3}); !res.OK {
		t.Fatalf("search failed: %s%s", res.Content, res.Error)
	}
	if !strings.Contains(got.body, `"max_results":3`) {
		t.Errorf("the limit did not reach the API: %s", got.body)
	}
}

// A search with no key is the operator's to fix, and the message has to say
// which variable and where — a model cannot set it and must not keep trying.
func TestWebSearchWithoutAKeySaysWhichVariableIsMissing(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "")
	a, s := userlandSession(t)

	res := searchWith(t, a, s, map[string]any{"query": "landlock go"})
	if res.OK {
		t.Fatalf("a search without a key succeeded: %s", res.Content)
	}
	for _, want := range []string{"TAVILY_API_KEY", ".env"} {
		if !strings.Contains(res.Error+res.Content, want) {
			t.Errorf("the failure does not mention %q: %s%s", want, res.Content, res.Error)
		}
	}
}

// An API that refuses the key is a different failure from one that has nothing
// to say, and neither is an empty result set.
func TestWebSearchReportsWhatTheAPISaid(t *testing.T) {
	url, _ := tavilyServer(t, `{"detail":{"error":"Unauthorized: missing or invalid API key"}}`, 401)
	t.Setenv("TAVILY_API_KEY", "tvly-wrong")
	t.Setenv("TAVILY_URL", url)
	a, s := userlandSession(t)
	allowServer(t, a, url)

	res := searchWith(t, a, s, map[string]any{"query": "landlock go"})
	if res.OK {
		t.Fatalf("a rejected key was reported as success: %s", res.Content)
	}
	if !strings.Contains(res.Error+res.Content, "401") {
		t.Errorf("the failure does not carry what the API said: %s%s", res.Content, res.Error)
	}
}

func TestWebSearchWithNoMatchesSaysSoRatherThanFailing(t *testing.T) {
	url, _ := tavilyServer(t, `{"query":"nothing at all","results":[]}`, 200)
	t.Setenv("TAVILY_API_KEY", "tvly-test-key")
	t.Setenv("TAVILY_URL", url)
	a, s := userlandSession(t)
	allowServer(t, a, url)

	res := searchWith(t, a, s, map[string]any{"query": "nothing at all"})
	if !res.OK {
		t.Fatalf("an empty result set was reported as a failure: %s%s", res.Content, res.Error)
	}
	if !strings.Contains(res.Content, "nothing at all") {
		t.Errorf("the answer does not name the query: %s", res.Content)
	}
}

// The key reaches the tool that declared it and no other. bash runs commands the
// model composed, and a credential in its environment is a credential the model
// can print — which is the whole reason a tool's environment is an allow-list.
func TestOnlyTheToolThatDeclaresTheKeyReceivesIt(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "tvly-secret-value")
	a, s := userlandSession(t)

	res := runShell(t, a, s, "env")
	if !res.OK {
		t.Fatalf("env failed: %s%s", res.Content, res.Error)
	}
	if strings.Contains(res.Content, "tvly-secret-value") || strings.Contains(res.Content, "TAVILY_API_KEY") {
		t.Error("the search key reached the shell, which runs commands the model wrote")
	}
}
