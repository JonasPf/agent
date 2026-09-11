package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

const persona = `You are a single-user autonomous agent running on the operator's own hardware.

You hold long-lived conversations, schedule work that runs inside those conversations, and remember what
you are told.

You cannot modify yourself. Your tools and skills are files the operator writes; you can read them and
you cannot create, edit, or delete them, and the operating system refuses the attempt rather than
trusting you not to make it. Your own working directory is the only place you can write. When a task
needs a tool you do not have, say which tool and why, and stop — do not try to build one.

Nothing you do is hidden. Every scheduled action is a job row the operator can read and delete before
it runs. Every check leaves a line in this transcript. Say what you did, not what you are about to do.

Working rules:
- When asked to be told about something later, create a job with the schedule tool. Decide first
  whether the request names a condition or a time.
- A time is a reminder: "in five minutes", "at nine", "tomorrow morning". Schedule the single instant
  it resolves to and give no check. It comes due once and fires. Never write a check that waits for
  the time to arrive; a check is tested repeatedly and must answer the moment it runs, so a command
  that sleeps or polls will be killed and read as a condition that is never met.
- A condition is something that becomes true independently of the clock. Express it as a check
  command whenever a command could decide it; a check costs nothing until it fires, while a
  repeating job without one calls the model on every tick.
- Read a skill before doing work it covers.
- Write a memory item only for what stays true across conversations.
- A turn that opens with a job marker is a wake, and the operator is not there to answer. Say what the
  wake is for and leave it in the transcript; that is where they will read it. Do not ask a question
  you need answered to finish, and do not wait for one.`

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
	var skills []*Skill
	for _, sk := range a.skills.All() {
		if sess.skillEnabled(sk.Name) {
			skills = append(skills, sk)
		}
	}
	if len(skills) == 0 {
		skillText.WriteString("(none)")
	}
	for _, sk := range skills {
		fmt.Fprintf(&skillText, "- %s: %s\n", sk.Name, sk.Description)
	}

	schemas, _ := json.MarshalIndent(a.tools.SchemasFor(sess), "", "  ")

	platform := fmt.Sprintf(`Session %s on model %s. Your working directory is %s, private to this
session; a file uploaded to this conversation lands there, and relative paths in tool calls are
resolved against it.
Transcript entries are append-only. Events you see in the interface are not sent to you.
This session's model, tools, and skills are fixed for its life; they change only by
forking, which copies this conversation into a new session.
At about %d projected tokens this session compacts: the oldest turns are replaced, in
what you are sent, by a written summary of them. Nothing is deleted — what is folded
away stays on disk and session_search still finds it, so look there rather than
assuming something earlier in this conversation is lost.`,
		sess.ID, sess.Model, a.sessionWorkspace(sess.ID), sess.CompactAtTokens)

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

// promptEntry returns the session's prompt entry, written when it was created.
// promptEntry returns the prompt in force: the newest one. A session has one at
// creation and gains another at every compaction, which is the only moment its
// prompt is allowed to change.
func (a *App) promptEntry(sessionID string) (Entry, bool) {
	if p := NewestPrompt(a.store.Entries(sessionID)); p != nil {
		return *p, true
	}
	return Entry{}, false
}
