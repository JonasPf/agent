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
