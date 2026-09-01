package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func memoryUsage(items []MemoryItem) int {
	n := 0
	for _, m := range items {
		n += len(m.Text)
	}
	return n
}

// AddMemory refuses a write that would exceed capacity and names what is stored,
// so consolidation can happen without a further lookup.
func (a *App) AddMemory(text, sourceSession string) (MemoryItem, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return MemoryItem{}, fmt.Errorf("empty memory item")
	}
	items, err := a.store.Memory()
	if err != nil {
		return MemoryItem{}, err
	}
	if memoryUsage(items)+len(text) > a.cfg.MemoryCapacity {
		return MemoryItem{}, fmt.Errorf(
			"memory is full: %d of %d characters used, and this item is %d. Remove or merge items first. Currently stored:\n%s",
			memoryUsage(items), a.cfg.MemoryCapacity, len(text), renderMemory(items))
	}
	m := MemoryItem{ID: newID(), Text: text, SourceSession: sourceSession, CreatedAt: time.Now()}
	if err := a.store.PutMemory(m); err != nil {
		return MemoryItem{}, err
	}
	return m, nil
}

func renderMemory(items []MemoryItem) string {
	var sb strings.Builder
	for _, m := range items {
		fmt.Fprintf(&sb, "  %s  %s\n", m.ID, m.Text)
	}
	if sb.Len() == 0 {
		return "  (nothing)"
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (a *App) memoryTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Text   string `json:"text"`
	}
	if err := decode(args, &in); err != nil {
		return nil, err
	}
	items, err := a.store.Memory()
	if err != nil {
		return nil, err
	}
	switch in.Action {
	case "list":
		return fmt.Sprintf("%d of %d characters used\n%s",
			memoryUsage(items), a.cfg.MemoryCapacity, renderMemory(items)), nil
	case "add":
		m, err := a.AddMemory(in.Text, tc.SessionID)
		if err != nil {
			return nil, err
		}
		a.appendEvent(tc.SessionID, Entry{EventKind: "memory_write", JobID: tc.JobID,
			Text: "remembered: " + m.Text})
		return "stored " + m.ID, nil
	case "edit":
		for _, m := range items {
			if m.ID == in.ID {
				m.Text = in.Text
				if err := a.store.PutMemory(m); err != nil {
					return nil, err
				}
				a.appendEvent(tc.SessionID, Entry{EventKind: "memory_write", JobID: tc.JobID,
					Text: "revised memory: " + m.Text})
				return "updated " + m.ID, nil
			}
		}
		return nil, fmt.Errorf("no memory item %s", in.ID)
	case "delete":
		if err := a.store.DeleteMemory(in.ID); err != nil {
			return nil, err
		}
		a.appendEvent(tc.SessionID, Entry{EventKind: "memory_write", JobID: tc.JobID,
			Text: "forgot item " + in.ID})
		return "deleted " + in.ID, nil
	}
	return nil, fmt.Errorf("unknown action %q", in.Action)
}
