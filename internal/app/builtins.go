package app

import (
	"context"
	"encoding/json"
	"strings"
)

// registerBuiltins registers the one capability that cannot live on disk.
// reload_tools is the registry's own control surface: a tool that decides which
// tools exist cannot depend on that decision having already been made.
// Everything else the model can call is a directory under the tool directory,
// reached through the HTTP API like any other client.
func (a *App) registerBuiltins() {
	a.tools.mu.Lock()
	defer a.tools.mu.Unlock()
	a.tools.register(&Tool{
		Name:        "reload_tools",
		Description: "Validate and register tools from disk.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}},
		Timeout:     60,
		Builtin:     true,
		run:         a.reloadTool,
	})
}

func (a *App) reloadTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	loaded, failures := a.ReloadTools(tc.SessionID)
	var sb strings.Builder
	sb.WriteString("loaded: " + strings.Join(loaded, ", ") + "\n")
	for _, f := range failures {
		sb.WriteString("failed " + f.Dir + ": " + f.Reason + "\n")
	}
	return sb.String(), nil
}
