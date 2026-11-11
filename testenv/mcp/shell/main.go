// Command shell is an MCP server over stdio with one tool, sh, which runs a
// shell command. It stands in for a bundle's server in the agent's own tests:
// what a bundle is, how it is started and confined, and how its answers come
// back are the agent's to test; what a real bundle does is tested where the
// bundle lives.
package main

import (
	"context"
	"log"
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
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
