package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A bundle's server may need the operator before it can answer a call: a
// name, a choice, or a sign-in somewhere else. MCP (2026-07-28) has it answer
// the call with what it needs instead of a result (multi round-trip requests),
// and the client try the same call again with the answers. Here the operator
// may answer days later, so nothing is held open: the turn ends on the
// question, the screen shows it under the call, and the answer arrives as the
// operator's message, after which the agent tries the call again and the turn
// carries on from what the tool then answers (ADR-094).

// busyWait is how long a server that is too busy to answer is first left
// before it is asked again; each try waits twice as long, three times in all.
var busyWait = 200 * time.Millisecond

// ToolQuestion is what a bundle's server asked before it could answer a call:
// the call to try again with the answers, what the server said to hand back
// with them, and each of its requests by the id the server gave it.
type ToolQuestion struct {
	Bundle       string                       `json:"bundle"`
	Arguments    json.RawMessage              `json:"arguments"`
	RequestState string                       `json:"request_state,omitempty"`
	Requests     map[string]*mcp.ElicitParams `json:"requests"`
}

// questionOf reads what a server asked. It may ask the operator to fill in a
// form or to go to an address; the deprecated requests (a model's answer, the
// client's roots) this agent does not offer, and a server that asks for them
// is refused.
func questionOf(b *bundle, args json.RawMessage, out *mcp.CallToolResult) (*ToolQuestion, error) {
	q := &ToolQuestion{Bundle: b.Name, Arguments: args, RequestState: out.RequestState,
		Requests: map[string]*mcp.ElicitParams{}}
	for id, req := range out.InputRequests {
		p, ok := req.(*mcp.ElicitParams)
		if !ok {
			return nil, fmt.Errorf("its server asked for %T, which this agent does not offer (MCP deprecates it)", req)
		}
		switch p.Mode {
		case "", "form":
			p.Mode = "form"
			if _, err := formSchema(p); err != nil {
				return nil, fmt.Errorf("its server asked a question whose form cannot be read: %v", err)
			}
		case "url":
			u, err := url.Parse(p.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return nil, fmt.Errorf("its server asked the operator to go to %q, which is not an https address", p.URL)
			}
		default:
			return nil, fmt.Errorf("its server asked a question of a kind this agent does not know (%q)", p.Mode)
		}
		q.Requests[id] = p
	}
	return q, nil
}

// formSchema is the schema a form question's answer must fit.
func formSchema(p *mcp.ElicitParams) (*jsonschema.Resolved, error) {
	raw, err := json.Marshal(p.RequestedSchema)
	if err != nil {
		return nil, err
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if s.Type != "object" {
		return nil, fmt.Errorf("a form is an object, not %q", s.Type)
	}
	return s.Resolve(nil)
}

// ids are the question's requests in a steady order.
func (q *ToolQuestion) ids() []string {
	ids := make([]string, 0, len(q.Requests))
	for id := range q.Requests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// toModel is what the model is told of a call that waits on the operator.
func (q *ToolQuestion) toModel(tool string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is waiting for the operator: its server asked them, and they are being shown it now:\n", tool)
	for _, id := range q.ids() {
		p := q.Requests[id]
		if p.Mode == "url" {
			u, _ := url.Parse(p.URL)
			fmt.Fprintf(&b, "- %s (they are sent to %s)\n", p.Message, u.Host)
		} else {
			fmt.Fprintf(&b, "- %s\n", p.Message)
		}
	}
	fmt.Fprintf(&b, "End your turn here. Their answer comes as their next message, and %s is tried again "+
		"with it then, so do not call it again for this.", tool)
	return b.String()
}

// questionAnswer is what the operator answered: accept, decline or cancel,
// and for accept, what each form request was filled in with.
type questionAnswer struct {
	Action  string                    `json:"action"`
	Content map[string]map[string]any `json:"content"`
}

// responses checks an answer against the question and makes what the server
// is handed with the call tried again.
func (q *ToolQuestion) responses(in questionAnswer) (mcp.InputResponseMap, error) {
	if !slices.Contains([]string{"accept", "decline", "cancel"}, in.Action) {
		return nil, fmt.Errorf("action is accept, decline or cancel, not %q", in.Action)
	}
	out := mcp.InputResponseMap{}
	for _, id := range q.ids() {
		r := &mcp.ElicitResult{Action: in.Action}
		if p := q.Requests[id]; in.Action == "accept" && p.Mode == "form" {
			schema, err := formSchema(p)
			if err != nil {
				return nil, err
			}
			content := in.Content[id]
			if content == nil {
				content = map[string]any{}
			}
			if err := schema.Validate(content); err != nil {
				return nil, fmt.Errorf("the answer to %q does not fit its form: %v", p.Message, err)
			}
			r.Content = content
		}
		out[id] = r
	}
	return out, nil
}

// said is the operator's message an answer arrives as.
func (q *ToolQuestion) said(tool string, in questionAnswer) string {
	switch in.Action {
	case "decline":
		return fmt.Sprintf("I declined %s's question.", tool)
	case "cancel":
		return fmt.Sprintf("I dismissed %s's question without answering.", tool)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "I answered %s's question.", tool)
	for _, id := range q.ids() {
		p := q.Requests[id]
		if p.Mode == "url" {
			fmt.Fprintf(&b, "\n%s: done.", p.Message)
			continue
		}
		keys := make([]string, 0, len(in.Content[id]))
		for k := range in.Content[id] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "\n%s: %v", k, in.Content[id][k])
		}
	}
	return b.String()
}

// questionRetry is the call a turn tries again before anything else, with the
// operator's answers.
type questionRetry struct {
	tool      string
	args      json.RawMessage
	responses mcp.InputResponseMap
	state     string
}

// answering keeps two answers to one question from both being taken.
var answering sync.Mutex

// hAnswerQuestion takes the operator's answer to what a tool's server asked,
// and queues the turn that tries the call again with it.
func (a *App) hAnswerQuestion(w http.ResponseWriter, r *http.Request) {
	sessionID, callID := r.PathValue("id"), r.PathValue("call")
	if a.store.Session(sessionID) == nil {
		fail(w, 404, "no such session")
		return
	}
	var in questionAnswer
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	answering.Lock()
	defer answering.Unlock()
	var asked *Entry
	for _, e := range a.store.Entries(sessionID) {
		if e.Role == "tool" && e.ToolCallID == callID && e.Question != nil {
			asked = &e
		}
		if asked != nil && e.EventKind == "question_answered" && e.ToolCallID == callID {
			fail(w, 409, "that question has been answered")
			return
		}
	}
	if asked == nil {
		fail(w, 404, "no call there is waiting on a question")
		return
	}
	q := asked.Question
	responses, err := q.responses(in)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	a.appendEvent(sessionID, Entry{EventKind: "question_answered", ToolCallID: callID, ToolName: asked.ToolName,
		Text: in.Action})
	retry := &questionRetry{tool: asked.ToolName, args: q.Arguments, responses: responses, state: q.RequestState}
	if err := a.sendTurn(sessionID, turnOpts{UserText: q.said(asked.ToolName, in), Retry: retry}); err != nil {
		fail(w, 404, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// retryCall tries a call again with the operator's answers, as the first
// thing a turn does: it is written as a call of the agent's own, so the model
// reads the tool's answer where it would have read it had nothing been asked.
func (a *App) retryCall(ctx context.Context, tc *ToolCtx, rt *questionRetry) (ToolCall, toolResult) {
	call := ToolCall{ID: fmt.Sprintf("retry_%d", time.Now().UnixNano()), Name: rt.tool, Arguments: string(rt.args)}
	t := a.tools.Get(rt.tool)
	if t == nil || t.bundle == nil {
		return call, errResult("tool %q is no longer installed, so the call could not be tried again", rt.tool)
	}
	return call, a.tools.callBundleWith(ctx, tc, t, rt.args, rt.responses, rt.state)
}
