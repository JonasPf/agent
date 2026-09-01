---
name: writing-tools
description: How to author a new tool for this agent.
---

A tool is a directory under `tools/`:

```
tools/deploys/
  manifest.json     required
  run               required, executable, any language
  schema.sql        optional, applied on load
  ui/panel.js       optional, an interface panel
```

`manifest.json`:

```json
{
  "name": "deploys",
  "description": "Query deployment status and history.",
  "db_prefix": "deploys_",
  "timeout_seconds": 30,
  "parameters": {
    "type": "object",
    "properties": { "action": { "type": "string", "enum": ["status", "history"] } },
    "required": ["action"]
  }
}
```

`description` is one sentence. `db_prefix` is unique across tools and every table the tool
creates begins with it. `parameters` is a JSON Schema object and reaches the model verbatim.

`run` receives the call arguments as JSON on stdin and writes JSON to stdout:

```
stdin   {"action": "status"}
stdout  {"ok": true, "content": "web: deployed 12m ago, healthy"}
        {"ok": false, "error": "service 'web' not found"}
```

Two environment variables are set: `AGENT_DB` (the SQLite path) and `AGENT_DB_PREFIX`.
Standard error is captured and stored with the result; the model does not see it.

After writing a tool, `chmod +x run` and call `reload_tools`. Validation registers the tool only if
the manifest parses, `run` is executable, and the prefix is unused; a failure names the reason and
leaves every other tool working. A successful reload commits the tool directory.

Build tools in their own conversation. A job attached to this conversation replays it on every
model tick, so tool-building work becomes recurring cost if it lives beside a job.
