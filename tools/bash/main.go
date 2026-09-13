// bash runs a shell command in the session's working directory. It is a shell,
// so it does what it is told; the boundary around it is the operating system's.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"agent/internal/tool"
)

type args struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

const maxOutput = 20000

// Clip caps a body and reports how much it dropped. An unannounced truncation
// is worse than a short answer: the model reasons from what it was handed as
// though that were all there was, and nothing in the result says otherwise.
func Clip(body string, max int) (string, int) {
	if len(body) <= max {
		return body, 0
	}
	return body[:max], len(body) - max
}

func main() {
	var a args
	tool.Args(&a)
	if strings.TrimSpace(a.Command) == "" {
		tool.Failf("command is required")
	}
	timeout := a.TimeoutSeconds
	if timeout <= 0 {
		timeout = 120
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", a.Command)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		tool.Failf("timed out after %ds", timeout)
	}
	body, dropped := Clip(string(out), maxOutput)
	if dropped > 0 {
		body += fmt.Sprintf("\n\n[output truncated: %d of %d characters shown, %d dropped]",
			maxOutput, maxOutput+dropped, dropped)
	}
	if err != nil {
		tool.Failf("%v\n%s", err, body)
	}
	if body == "" {
		body = "(no output)"
	}
	tool.OK(body)
}
