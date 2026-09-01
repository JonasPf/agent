package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

const persona = `You are a single-user autonomous agent running on the operator's own hardware.

You hold long-lived conversations, schedule work that runs inside those conversations, remember what
you are told, and write your own tools when the ones you have are not enough.

Nothing you do is hidden. Every scheduled action is a job row the operator can read and delete before
it runs. Every check leaves a line in this transcript. Say what you did, not what you are about to do.

Working rules:
- When asked to be told about something later, create a job with the schedule tool. Express the
  condition as a check command whenever a command could decide it; a check costs nothing until it
  fires, while a job without one calls the model on every tick.
- Read a skill before doing work it covers.
- Write a memory item only for what stays true across conversations.
- Call notify when something is worth interrupting the operator for. In a job without a check,
  calling notify is what marks the condition met.`

// systemSections builds the prompt as sent, split for display. Tool definitions
// are shown here exactly as the model receives them; they travel in the tools array.
func (a *App) systemSections(sess *Session) []Section {
	mem, _ := a.store.Memory()
	var memText strings.Builder
	if len(mem) == 0 {
		memText.WriteString("(empty)")
	}
	for _, m := range mem {
		fmt.Fprintf(&memText, "- %s\n", m.Text)
	}

	var skillText strings.Builder
	skills := a.skills.All()
	if len(skills) == 0 {
		skillText.WriteString("(none)")
	}
	for _, s := range skills {
		fmt.Fprintf(&skillText, "- %s: %s\n", s.Name, s.Description)
	}

	schemas, _ := json.MarshalIndent(a.tools.Schemas(), "", "  ")

	platform := fmt.Sprintf(`Session %s on model %s. Workspace is %s.
Transcript entries are append-only. Events you see in the interface are not sent to you.
This session rotates into a successor at about %d projected tokens.`,
		sess.ID, sess.Model, a.cfg.Workspace, sess.RotateAtTokens)

	secs := []Section{
		{Name: "persona", Text: persona},
		{Name: "memory", Text: strings.TrimRight(memText.String(), "\n"), Editable: true},
		{Name: "skills_index", Text: strings.TrimRight(skillText.String(), "\n")},
		{Name: "platform", Text: platform},
		{Name: "tool_schemas", Text: string(schemas)},
	}
	for i := range secs {
		secs[i].Tokens = estTokens(secs[i].Text)
	}
	return secs
}

// systemMessage renders the sections the model actually receives as text.
// Tool definitions are excluded because they travel in the tools array.
func systemMessage(sections []Section) string {
	var sb strings.Builder
	for _, s := range sections {
		if s.Name == "tool_schemas" {
			continue
		}
		fmt.Fprintf(&sb, "## %s\n%s\n\n", strings.ReplaceAll(s.Name, "_", " "), s.Text)
	}
	return strings.TrimSpace(sb.String())
}

func sectionsTotal(sections []Section) int {
	n := 0
	for _, s := range sections {
		n += s.Tokens
	}
	return n
}

// promptEntry returns the session's stored prompt entry, which is fixed for its life.
func (a *App) promptEntry(sessionID string) (Entry, bool) {
	for _, e := range a.store.Entries(sessionID) {
		if e.EventKind == "prompt" {
			return e, true
		}
	}
	return Entry{}, false
}
