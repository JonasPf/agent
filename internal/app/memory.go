package app

import (
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
