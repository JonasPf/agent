package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// richManifest is the stand-in server as a bundle whose tools answer with
// every kind of part and a structured result (testenv/mcp/shell, "rich").
const richManifest = `{
  "manifest_version": "0.3",
  "name": "rich",
  "version": "0.1.0",
  "description": "Every kind of answer, for the tests.",
  "author": {"name": "the tests"},
  "server": {
    "type": "binary",
    "entry_point": "server/shell",
    "mcp_config": {"command": "${__dirname}/server/shell", "args": ["rich"]}
  },
  "compatibility": {"platforms": ["linux"]},
  "_meta": {"io.github.jonaspf.agent": {"timeout_seconds": 5}}
}`

// The bytes the stand-in answers parts with (testenv/mcp/shell, richServer).
var (
	richPicture = []byte("\x89PNG\r\n\x1a\n a picture")
	richSound   = []byte("RIFF a sound")
	richBlob    = []byte{0, 1, 2, 3}
)

// "A part of a bundle tool's result that is not text (a picture, a sound, a
// resource's bytes) is saved in the conversation's directory under
// tool-output/, never over an earlier one, and the model is sent its path and
// type. A resource's text is sent as text, and a link to a resource is named
// with its address."
func TestPartsOfAResultThatAreNotTextArriveAsFiles(t *testing.T) {
	bundles := t.TempDir()
	writeBundle(t, bundles, "rich", richManifest)
	a, failures := bundleApp(t, bundles)
	if len(failures) > 0 {
		t.Fatalf("the bundle did not load: %+v", failures)
	}
	srv := scriptedModel(t, toolStep{tool: "parts", args: "{}"}, toolStep{tool: "parts", args: "{}"}, toolStep{text: "Done."})
	a.or.base = srv.URL
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err := a.SendUserMessage(s.ID, "show me the parts"); err != nil {
		t.Fatal(err)
	}
	settle(t, a, s.ID)

	var results []toolResult
	for _, e := range a.store.Entries(s.ID) {
		if e.Role == "tool" && e.ToolName == "parts" {
			var res toolResult
			_ = json.Unmarshal(e.ToolResult, &res)
			results = append(results, res)
		}
	}
	if len(results) != 2 {
		t.Fatalf("%d results of parts, want 2", len(results))
	}
	first := results[0]
	for _, want := range []string{"Here they are.",
		"image/png", "tool-output/parts-1.png",
		"audio/wav", "tool-output/parts-2.wav",
		"file:///notes.txt", "a note",
		"file:///blob.bin", "tool-output/parts-3.bin",
		"file:///far.txt", "A file far away."} {
		if !first.OK || !strings.Contains(first.Content, want) {
			t.Errorf("the result does not say %q:\n%+v", want, first)
		}
	}
	dir := a.ensureWorkspace(s.ID)
	for name, want := range map[string][]byte{"parts-1.png": richPicture, "parts-2.wav": richSound, "parts-3.bin": richBlob,
		"parts-4.png": richPicture, "parts-5.wav": richSound, "parts-6.bin": richBlob} {
		got, err := os.ReadFile(filepath.Join(dir, "tool-output", name))
		if err != nil || string(got) != string(want) {
			t.Errorf("tool-output/%s = %q, %v; want the part's bytes", name, got, err)
		}
	}
	if !strings.Contains(results[1].Content, "tool-output/parts-4.png") {
		t.Errorf("the second call's picture is not saved beside the first's:\n%s", results[1].Content)
	}
}

// "A result whose structured part does not match the output schema its tool
// declares says so in what the model is sent."
func TestAStructuredResultIsCheckedAgainstItsToolsSchema(t *testing.T) {
	bundles := t.TempDir()
	writeBundle(t, bundles, "rich", richManifest)
	a, failures := bundleApp(t, bundles)
	if len(failures) > 0 {
		t.Fatalf("the bundle did not load: %+v", failures)
	}
	s, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	call := func(args string) toolResult {
		return a.tools.Call(context.Background(), &ToolCtx{App: a, SessionID: s.ID}, "typed", json.RawMessage(args))
	}
	if res := call(`{}`); !res.OK || !strings.Contains(res.Content, `{"n":1}`) || strings.Contains(res.Content, "schema") {
		t.Errorf("a result that matches = %+v, want it as it came", res)
	}
	if res := call(`{"wrong":true}`); !strings.Contains(res.Content+res.Error, "does not match the output schema typed declares") {
		t.Errorf("a result that does not match = %+v, want that said", res)
	}
}

// "A bundle's server instructions are in the prompt of a conversation that has
// any of the bundle's tools, under the bundle's name, and in no other."
func TestAServersInstructionsAreInThePromptOfAConversationWithItsTools(t *testing.T) {
	bundles := t.TempDir()
	writeBundle(t, bundles, "rich", richManifest)
	writeBundle(t, bundles, "shell", shellManifest)
	a, failures := bundleApp(t, bundles)
	if len(failures) > 0 {
		t.Fatalf("the bundles did not load: %+v", failures)
	}
	with, _ := a.NewSession(SessionConfig{Model: "test/model"}, "")
	text, ok := promptSection(t, a, with, "server_instructions")
	if !ok || !strings.Contains(text, "### rich\nCall parts before anything else.") {
		t.Errorf("server_instructions = %q, %v; want rich's under its name", text, ok)
	}
	if strings.Contains(text, "### shell") {
		t.Errorf("a server that gives no instructions has a heading: %q", text)
	}
	without, _ := a.NewSession(SessionConfig{Model: "test/model", EnabledTools: []string{"sh"}}, "")
	if text, ok := promptSection(t, a, without, "server_instructions"); ok {
		t.Errorf("a conversation without rich's tools has its instructions: %q", text)
	}
}

// "The Plugins screen gives a bundle tool's title, and what its server says of
// it (read-only, destructive, idempotent, reaching outside), only where the
// server said it; nothing is decided from these, which are its word."
func TestAToolsTitleAndHintsAreItsServers(t *testing.T) {
	bundles := t.TempDir()
	writeBundle(t, bundles, "rich", richManifest)
	writeBundle(t, bundles, "shell", shellManifest)
	a, failures := bundleApp(t, bundles)
	if len(failures) > 0 {
		t.Fatalf("the bundles did not load: %+v", failures)
	}
	tools, _ := listedTools(t, a)
	hints := func(name string) string {
		raw, _ := json.Marshal(tools[name]["hints"])
		return string(raw)
	}
	if got := tools["parts"]["title"]; got != "Every part" {
		t.Errorf("parts' title = %v, want its server's", got)
	}
	if got := hints("parts"); got != `["read-only"]` {
		t.Errorf("parts' hints = %s, want read-only alone (closed to the world is no warning)", got)
	}
	if got := hints("typed"); got != `["destructive","idempotent","open-world"]` {
		t.Errorf("typed's hints = %s", got)
	}
	if _, ok := tools["sh"]["title"]; ok {
		t.Errorf("sh has a title its server never gave: %v", tools["sh"])
	}
	if got := hints("sh"); got != "null" {
		t.Errorf("sh's hints = %s, want none", got)
	}
}
