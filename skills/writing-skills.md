---
name: writing-skills
description: What belongs in a skill, and when a lesson is a skill rather than a tool.
---

A skill is a document that teaches one thing. It is prose, not code.

Write one after work that required figuring something out, so the next attempt starts from the
answer. Store it as `skills/<name>.md` with frontmatter:

```
---
name: kebab-case-name
description: One line. Only this and the name reach the system prompt.
---
```

Only the name and description are in context. The body is loaded by `skill_read` when it is
judged relevant, which is what lets the collection grow.

A lesson is a skill when it is judgement: when to reach for something, what usually goes wrong,
which order the steps go in. It is a tool when it is a capability the agent cannot otherwise
perform. Prefer the skill: a poor skill is poor prose, whereas a poor tool is an executable that
fails at three in the morning.

Say what is true in the fewest words that stay unambiguous. Do not narrate history, restate the
code, or describe the state of the work.
