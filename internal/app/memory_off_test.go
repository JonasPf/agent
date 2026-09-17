package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// Memory can be off for one conversation. Off means both halves: no memory
// section in the prompt, and no tool to write one with — an item written into a
// conversation that cannot read it back is stored, invisible, and not obviously
// either.

// registerMemoryTool puts the memory tool in the registry. A test app loads no
// tools from disk, and what this is about is whether the session is offered one.
func registerMemoryTool(a *App) {
	a.tools.register(&Tool{Name: memoryTool, Builtin: true,
		Description: "Write, revise, or remove durable memory.",
		Parameters:  map[string]any{"type": "object"}})
}

func TestAConversationWithMemoryOnHasASectionAndTheTool(t *testing.T) {
	a := newTestApp(t)
	registerMemoryTool(a)
	// A test app carries no capacity, and a store with none refuses every write.
	a.cfg.MemoryCapacity = defaultMemoryCapacity
	if _, err := a.AddMemory("prefers Go", ""); err != nil {
		t.Fatal(err)
	}
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	text, ok := promptSection(t, a, s, "memory")
	if !ok {
		t.Fatal("a conversation with memory on has no memory section")
	}
	if !strings.Contains(text, "prefers Go") {
		t.Errorf("memory section = %q, want the stored item", text)
	}
	if schemas, _ := promptSection(t, a, s, "tool_schemas"); !strings.Contains(schemas, memoryTool) {
		t.Error("a conversation with memory on is not offered the memory tool")
	}
}

func TestAConversationWithMemoryOffHasNoSectionAndNoTool(t *testing.T) {
	a := newTestApp(t)
	registerMemoryTool(a)
	// A test app carries no capacity, and a store with none refuses every write.
	a.cfg.MemoryCapacity = defaultMemoryCapacity
	if _, err := a.AddMemory("prefers Go", ""); err != nil {
		t.Fatal(err)
	}
	s, err := a.NewSession(SessionConfig{Model: "test/model", MemoryOff: true}, "")
	if err != nil {
		t.Fatal(err)
	}

	if text, ok := promptSection(t, a, s, "memory"); ok {
		t.Errorf("a conversation with memory off still has a memory section: %q", text)
	}
	// Not merely absent from the section: absent from the whole prompt, or the
	// stored item would still be steering the conversation.
	p, _ := a.promptEntry(s.ID)
	for _, sec := range p.Sections {
		if strings.Contains(sec.Text, "prefers Go") {
			t.Errorf("a stored memory reached a conversation with memory off, in %s: %q", sec.Name, sec.Text)
		}
	}
	schemas, _ := promptSection(t, a, s, "tool_schemas")
	if strings.Contains(schemas, memoryTool) {
		t.Errorf("a conversation with memory off is offered the memory tool:\n%s", schemas)
	}
	// And the conversation is told, so it says no rather than agreeing to
	// remember something it cannot.
	if platform, _ := promptSection(t, a, s, "platform"); !strings.Contains(platform, "Memory is off") {
		t.Errorf("the conversation is not told memory is off:\n%s", platform)
	}
}

// The tools screen has to agree with the prompt, or the operator reads that a
// tool is available in a conversation that was never offered it.
func TestTheToolsListSaysMemoryIsOffForThatConversation(t *testing.T) {
	a := newTestApp(t)
	registerMemoryTool(a)
	off, err := a.NewSession(SessionConfig{Model: "test/model", MemoryOff: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	on, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id   string
		want bool
	}{{off.ID, false}, {on.ID, true}} {
		w := callAPI(t, a, "GET", "/tools?session_id="+c.id, nil)
		var got struct {
			Tools []struct {
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v (%s)", err, w.Body.String())
		}
		var found bool
		for _, tl := range got.Tools {
			if tl.Name != memoryTool {
				continue
			}
			found = true
			if tl.Enabled != c.want {
				t.Errorf("session %s: memory enabled = %v, want %v", c.id, tl.Enabled, c.want)
			}
		}
		if !found {
			t.Fatalf("the memory tool is not listed at all: %s", w.Body.String())
		}
	}
}

func TestMemoryIsFixedForASessionAndChangesByForking(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	w := callAPI(t, a, "PATCH", "/sessions/"+s.ID, map[string]any{"memory_off": true})
	if w.Code != 409 {
		t.Fatalf("PATCH memory_off: status %d, want 409 (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "fork") {
		t.Errorf("the refusal does not name forking: %s", w.Body.String())
	}

	cfg := s.SessionConfig
	cfg.MemoryOff = true
	fork, err := a.Fork(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := promptSection(t, a, fork, "memory"); ok {
		t.Error("the fork still has a memory section")
	}
	var said bool
	for _, e := range a.store.Entries(fork.ID) {
		if strings.Contains(e.Text, "memory on → off") {
			said = true
		}
	}
	if !said {
		t.Error("the fork does not record that memory was turned off")
	}
}

// A session stored before this setting existed has no memory_off field, and must
// read back with memory on — the state it was actually in.
func TestASessionStoredBeforeTheSettingExistedKeepsItsMemory(t *testing.T) {
	a := newTestApp(t)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.MemoryOff {
		t.Fatal("a session created without the setting has memory off")
	}
	reloaded := a.store.Session(s.ID)
	if reloaded.MemoryOff {
		t.Error("memory turned itself off across a round trip through storage")
	}
}
