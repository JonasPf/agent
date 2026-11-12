package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// web_search reaches a search API, and the key it needs is the operator's, set
// once for its bundle rather than granted to a conversation (ADR-087). This
// drives the real bundle with the API substituted — the one thing R5 allows
// to be, alongside the model provider. What the tool does with an answer is
// tested where the bundle lives.

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

// "A value the operator set for a bundle reaches that bundle's server and no
// other." The shell runs commands the model composed, and a credential in its
// environment is one the model can print.
func TestABundlesValueReachesItsOwnToolAndNoOther(t *testing.T) {
	url, got := tavilyServer(t, tavilyBody, 200)
	a, s := userlandSession(t)
	allowServer(t, a, url)
	set := map[string]any{"tavily_api_key": "tvly-secret-value", "tavily_url": url}
	if w := sendJSON(t, a, "PUT", "/plugins/web-search/config", set); w.Code != 200 {
		t.Fatalf("config = %d %s", w.Code, w.Body)
	}

	res := searchWith(t, a, s, map[string]any{"query": "landlock go", "limit": 5})
	if !res.OK || !strings.Contains(res.Content, "pkg.go.dev/landlock") {
		t.Fatalf("search = %+v, want what the API answered", res)
	}
	if !got.made || !strings.Contains(fmt.Sprint(got.header)+got.body, "tvly-secret-value") {
		t.Errorf("the API was not asked with the key that was set: %v %s", got.header, got.body)
	}

	res = runShell(t, a, s, "env")
	if !res.OK {
		t.Fatalf("env failed: %s%s", res.Content, res.Error)
	}
	if strings.Contains(res.Content, "tvly-secret-value") || strings.Contains(res.Content, "TAVILY_API_KEY") {
		t.Error("the search key reached the shell, which runs commands the model wrote")
	}
}
