// call_model makes one call to another model: one that writes, speaks,
// transcribes, or decides. The agent makes the call with its own key and adds
// the cost to the conversation's (ADR-070); this hands it the request, naming
// files by path, and reports what came back.
package main

import (
	"encoding/json"
	"time"

	"agent/internal/tool"
)

type args struct {
	Model      string          `json:"model,omitempty"`
	Prompt     string          `json:"prompt,omitempty"`
	Files      []string        `json:"files,omitempty"`
	Voice      string          `json:"voice,omitempty"`
	Output     string          `json:"output,omitempty"`
	Language   string          `json:"language,omitempty"`
	Timestamps bool            `json:"timestamps,omitempty"`
	Reasoning  string          `json:"reasoning,omitempty"`
	Questions  json.RawMessage `json:"questions,omitempty"`
	List       string          `json:"list,omitempty"`
}

func main() {
	var a args
	tool.Args(&a)
	if a.Model == "" && a.List == "" {
		tool.Failf("name a model, or set list to text, speech, transcription, or decisions to find one")
	}
	// A transcription of a long recording, or a model that reasons, can take
	// minutes to answer; the manifest's timeout is the bound.
	tool.SetAPITimeout(10 * time.Minute)
	var res struct {
		Content string `json:"content"`
	}
	tool.API("POST", "/model", a, nil, &res)
	tool.OK(res.Content)
}
