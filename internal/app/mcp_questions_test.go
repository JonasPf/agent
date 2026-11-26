package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// askManifest is the stand-in server as a bundle whose tools need the
// operator before they answer (testenv/mcp/shell, "ask").
const askManifest = `{
  "manifest_version": "0.3",
  "name": "ask",
  "version": "0.1.0",
  "description": "Tools that ask, for the tests.",
  "author": {"name": "the tests"},
  "server": {
    "type": "binary",
    "entry_point": "server/shell",
    "mcp_config": {"command": "${__dirname}/server/shell", "args": ["ask"]}
  },
  "compatibility": {"platforms": ["linux"]},
  "_meta": {"io.github.jonaspf.agent": {"timeout_seconds": 5}}
}`

// askTurn is an agent with the ask bundle and a conversation that has just
// called tool, on a model that then says steps.
func askTurn(t *testing.T, tool string, after ...toolStep) (*App, *Session) {
	t.Helper()
	bundles := t.TempDir()
	writeBundle(t, bundles, "ask", askManifest)
	a, failures := bundleApp(t, bundles)
	if len(failures) > 0 {
		t.Fatalf("the bundle did not load: %+v", failures)
	}
	srv := scriptedModel(t, append([]toolStep{{tool: tool, args: "{}"}}, after...)...)
	a.or.base = srv.URL
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err := a.SendUserMessage(s.ID, "use "+tool); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)
	return a, s
}

// asked is the call of tool that holds a question, and what its result said.
func asked(t *testing.T, a *App, s *Session, tool string) (Entry, toolResult) {
	t.Helper()
	for _, e := range a.store.Entries(s.ID) {
		if e.Role == "tool" && e.ToolName == tool && e.Question != nil {
			var res toolResult
			_ = json.Unmarshal(e.ToolResult, &res)
			return e, res
		}
	}
	t.Fatalf("no call of %s holds a question: %+v", tool, a.store.Entries(s.ID))
	return Entry{}, toolResult{}
}

func answer(t *testing.T, a *App, s *Session, callID string, body any) int {
	t.Helper()
	return callAPI(t, a, "POST", "/sessions/"+s.ID+"/questions/"+callID, body).Code
}

// "A tool whose server needs the operator's answer ends the turn: the model is
// told the tool is waiting for the operator, and the call holds the question
// for the screen. The operator's answer arrives as their message; the agent
// tries the call again with it, and the turn carries on from what the tool
// then answers. A question is answered once."
func TestAToolsQuestionEndsTheTurnAndTheAnswerCarriesItOn(t *testing.T) {
	a, s := askTurn(t, "greet", toolStep{text: "Not reached."}, toolStep{text: "Greeted."})
	e, res := asked(t, a, s, "greet")
	if !res.OK || !strings.Contains(res.Content, "What is your name?") || !strings.Contains(res.Content, "waiting for the operator") {
		t.Errorf("the model was told %+v, want the question and that the tool waits for the operator", res)
	}
	q := e.Question
	if q.Bundle != "ask" || len(q.Requests) != 1 || q.Requests["name"] == nil {
		t.Errorf("the question = %+v, want ask's one request, name", q)
	}
	entries := a.store.Entries(s.ID)
	if last := entries[len(entries)-1]; last.Seq != e.Seq {
		t.Fatalf("the turn went on after the question: %+v", last)
	}

	if code := answer(t, a, s, e.ToolCallID, map[string]any{"action": "accept",
		"content": map[string]any{"name": map[string]any{"name": "Jonas", "times": 2}}}); code != http.StatusOK {
		t.Fatalf("answering = %d, want 200", code)
	}
	settle(t, a, s.ID)
	var said, retried, ended bool
	for _, x := range a.store.Entries(s.ID) {
		if x.Seq <= e.Seq {
			continue
		}
		switch {
		case x.Role == "user" && strings.Contains(x.Text, "I answered greet's question") && strings.Contains(x.Text, "Jonas"):
			said = true
		case x.Role == "tool" && x.ToolName == "greet" && strings.Contains(string(x.ToolResult), "Hello, Jonas, 2 times (state asked-once)."):
			retried = true
		case x.Role == "assistant" && x.Text == "Greeted.":
			ended = true
		}
	}
	if !said || !retried || !ended {
		t.Errorf("after the answer: said %v, tried again %v, carried on %v:\n%+v", said, retried, ended, a.store.Entries(s.ID))
	}
	if code := answer(t, a, s, e.ToolCallID, map[string]any{"action": "decline"}); code != http.StatusConflict {
		t.Errorf("answering twice = %d, want 409", code)
	}
}

// "An answer that does not fit the question's form, or is neither accept,
// decline nor cancel, is refused, and nothing is tried."
func TestAnAnswerThatDoesNotFitIsRefused(t *testing.T) {
	a, s := askTurn(t, "greet")
	e, _ := asked(t, a, s, "greet")
	before := len(a.store.Entries(s.ID))
	for _, body := range []map[string]any{
		{"action": "accept", "content": map[string]any{"name": map[string]any{"times": 2}}},
		{"action": "accept", "content": map[string]any{"name": map[string]any{"name": "Jonas", "times": 0}}},
		{"action": "maybe"},
	} {
		if code := answer(t, a, s, e.ToolCallID, body); code != http.StatusBadRequest {
			t.Errorf("answering %v = %d, want 400", body, code)
		}
	}
	if code := answer(t, a, s, "no-such-call", map[string]any{"action": "decline"}); code != http.StatusNotFound {
		t.Errorf("answering a call that asked nothing = %d, want 404", code)
	}
	if after := len(a.store.Entries(s.ID)); after != before {
		t.Errorf("a refused answer wrote %d entries", after-before)
	}
}

// "Declining is said to the server, which answers as it will."
func TestDecliningAToolsQuestionIsSaidToItsServer(t *testing.T) {
	a, s := askTurn(t, "greet")
	e, _ := asked(t, a, s, "greet")
	if code := answer(t, a, s, e.ToolCallID, map[string]any{"action": "decline"}); code != http.StatusOK {
		t.Fatalf("declining = %d", code)
	}
	settle(t, a, s.ID)
	var said, told bool
	for _, x := range a.store.Entries(s.ID) {
		said = said || x.Role == "user" && strings.Contains(x.Text, "I declined greet's question")
		told = told || x.Role == "tool" && strings.Contains(string(x.ToolResult), "No name given (decline).")
	}
	if !said || !told {
		t.Errorf("declined: said %v, server told %v:\n%+v", said, told, a.store.Entries(s.ID))
	}
}

// "A question that sends the operator to an address holds the address; once
// they say they are done there, the call is tried again."
func TestAQuestionThatSendsTheOperatorToAnAddress(t *testing.T) {
	a, s := askTurn(t, "connect")
	e, res := asked(t, a, s, "connect")
	raw, _ := json.Marshal(e.Question.Requests["login"])
	if !strings.Contains(string(raw), `"mode":"url"`) || !strings.Contains(string(raw), "https://example.com/authorize?client=agent") {
		t.Errorf("the question = %s, want the address", raw)
	}
	if !strings.Contains(res.Content, "example.com") {
		t.Errorf("the model was not told where the operator was sent: %s", res.Content)
	}
	if code := answer(t, a, s, e.ToolCallID, map[string]any{"action": "accept"}); code != http.StatusOK {
		t.Fatalf("done = %d", code)
	}
	settle(t, a, s.ID)
	found := false
	for _, x := range a.store.Entries(s.ID) {
		found = found || x.Role == "tool" && strings.Contains(string(x.ToolResult), "Connected: accept.")
	}
	if !found {
		t.Errorf("the call was not tried again: %+v", a.store.Entries(s.ID))
	}
}

// "A server too busy to answer is tried again with what it said, and its
// answer is the result."
func TestABusyServerIsTriedAgain(t *testing.T) {
	bundles := t.TempDir()
	writeBundle(t, bundles, "ask", askManifest)
	a, _ := bundleApp(t, bundles)
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	res := a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "busy", json.RawMessage(`{}`))
	if !res.OK || res.Content != "Done, after a wait." {
		t.Errorf("busy = %+v, want its answer once tried again", res)
	}
}
