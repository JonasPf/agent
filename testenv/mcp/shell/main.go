// Command shell is an MCP server over stdio with one tool, sh, which runs a
// shell command. It stands in for a bundle's server in the agent's own tests:
// what a bundle is, how it is started and confined, and how its answers come
// back are the agent's to test; what a real bundle does is tested where the
// bundle lives.
//
// Given a name, a description and a script, it offers one tool of that name
// instead, which runs the script with the call's arguments on standard input.
// What the script prints is the answer; printed as {"ok": false, "error": …}
// it is an error, and as {"ok": true, "content": …} it is that content. That
// is how a test makes a tool that does one thing in a few lines.
//
// Given the one argument "apps", it is a server with a page: show, whose result
// a host that takes MCP Apps opens ui://shell/page for; refresh, which only the
// page may call; and quiet, which only the model may.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type args struct {
	Command string `json:"command" jsonschema:"the shell command to run"`
}

func main() {
	s := mcp.NewServer(&mcp.Implementation{Name: "shell", Version: "0.0.1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "sh", Description: "Run a shell command in the conversation's directory."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
			out, err := exec.CommandContext(ctx, "/bin/sh", "-c", a.Command).CombinedOutput()
			res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}
			if err != nil {
				res.IsError = true
				res.Content = append(res.Content, &mcp.TextContent{Text: err.Error()})
			}
			return res, nil, nil
		})
	if len(os.Args) == 2 && os.Args[1] == "apps" {
		s = appsServer()
	}
	if len(os.Args) == 4 {
		s = mcp.NewServer(&mcp.Implementation{Name: os.Args[1], Version: "0.0.1"}, nil)
		s.AddTool(&mcp.Tool{Name: os.Args[1], Description: os.Args[2],
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}, script(os.Args[3]))
	}
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

func script(run string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cmd := exec.CommandContext(ctx, run)
		cmd.Stdin = bytes.NewReader(req.Params.Arguments)
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		text, failed := string(out), err != nil
		var said struct {
			OK      *bool           `json:"ok"`
			Content json.RawMessage `json:"content"`
			Error   string          `json:"error"`
		}
		if json.Unmarshal(out, &said) == nil && said.OK != nil {
			text, failed = said.Error, !*said.OK
			if *said.OK {
				text = string(said.Content)
				_ = json.Unmarshal(said.Content, &text)
			}
		}
		if err != nil {
			text += err.Error()
		}
		return &mcp.CallToolResult{IsError: failed, Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

const appMime = "text/html;profile=mcp-app"

// appPage is the page show opens. It does what a page is allowed to and tries
// what it is not, and says how each went in one message to the conversation.
const appPage = `<!doctype html><html><body><p id="state">waiting</p><script>
var next = 1, waiting = {};
function request(method, params) {
  var id = next++;
  parent.postMessage({jsonrpc: "2.0", id: id, method: method, params: params}, "*");
  return new Promise(function (ok, no) { waiting[id] = [ok, no]; });
}
function notify(method, params) { parent.postMessage({jsonrpc: "2.0", method: method, params: params}, "*"); }
var input = null;
addEventListener("message", function (e) {
  var m = e.data || {};
  if (m.id !== undefined && waiting[m.id]) {
    var w = waiting[m.id]; delete waiting[m.id];
    if (m.error) w[1](new Error(m.error.message)); else w[0](m.result);
    return;
  }
  if (m.method === "ui/notifications/tool-input") input = m.params.arguments;
  if (m.method === "ui/notifications/tool-result") shown(m.params);
});
function shown(result) {
  var said = "value=" + result.structuredContent.value + " note=" + input.note;
  request("tools/call", {name: "refresh", arguments: {}}).then(function (r) {
    said += " refresh=" + r.content[0].text;
    return request("tools/call", {name: "quiet", arguments: {}}).then(function () { return "allowed"; }, function () { return "refused"; });
  }).then(function (r) {
    said += " quiet=" + r;
    return fetch(input.probe, {method: "POST", mode: "no-cors", body: "{}"}).then(function () { return "reached"; }, function () { return "blocked"; });
  }).then(function (r) {
    said += " fetch=" + r;
    document.getElementById("state").textContent = said;
    return request("ui/message", {role: "user", content: {type: "text", text: said}});
  });
}
request("ui/initialize", {appCapabilities: {}, appInfo: {name: "page", version: "1"}, protocolVersion: "2026-01-26"}).then(function () {
  notify("ui/notifications/initialized", {});
});
</script></body></html>`

func appsServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "apps", Version: "0.0.1"}, nil)
	schema := json.RawMessage(`{"type":"object","properties":{"note":{"type":"string","description":"A note."},"probe":{"type":"string","description":"An address."}}}`)
	text := func(t string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: t}}}
	}
	s.AddTool(&mcp.Tool{Name: "show", Description: "Show a page.", InputSchema: schema,
		Meta: mcp.Meta{"ui": map[string]any{"resourceUri": "ui://shell/page"}}},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res := text("The page is shown.")
			res.StructuredContent = map[string]any{"value": 42}
			return res, nil
		})
	s.AddTool(&mcp.Tool{Name: "refresh", Description: "Refresh the page.", InputSchema: schema,
		Meta: mcp.Meta{"ui": map[string]any{"resourceUri": "ui://shell/page", "visibility": []string{"app"}}}},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_ = os.WriteFile("refreshed.txt", []byte("by the page\n"), 0o644)
			return text("fresh"), nil
		})
	s.AddTool(&mcp.Tool{Name: "quiet", Description: "For the model only.", InputSchema: schema,
		Meta: mcp.Meta{"ui": map[string]any{"visibility": []string{"model"}}}},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return text("quiet"), nil
		})
	s.AddResource(&mcp.Resource{URI: "ui://shell/page", Name: "page", MIMEType: appMime},
		func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: "ui://shell/page", MIMEType: appMime, Text: appPage}}}, nil
		})
	return s
}
