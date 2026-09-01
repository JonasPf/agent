# agent

A single-user autonomous agent. One process, one container, one user.
Implements [specs/](specs/index.html); prototype, not hardened.

## Run

```sh
export OPENROUTER_API_KEY=sk-or-...
go run ./cmd/agent
```

Open http://localhost:8080. Add to the home screen for notifications.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPENROUTER_API_KEY` | — | Required for model calls. |
| `AGENT_ADDR` | `:8080` | Listen address. |
| `AGENT_MODEL` | `anthropic/claude-sonnet-4.5` | Default model for a new session. |
| `AGENT_DATA` | `data` | SQLite plus one directory per session. |
| `AGENT_WORKSPACE` | `workspace` | Where the agent's file and shell tools operate. |
| `AGENT_TOOLS` / `AGENT_SKILLS` | `tools` / `skills` | Scanned at start and on reload. |
| `AGENT_ROTATE_TOKENS` | `40000` | Projected size at which a session rotates. |
| `AGENT_MEMORY_CAPACITY` | `8000` | Characters of durable memory. |

The working directory becomes a git repository at first start, and every successful tool reload
commits the tool directory.

## Layout

```
cmd/agent          entry point
internal/app
  loop.go          assemble, call, dispatch tools, repeat
  projection.go    transcript → messages (pure; tested)
  sessions.go      prompt entry, availability, summary, rotation, fork
  sched.go         ticks, checks, dead letters, circuit breaker
  tools.go         manifests, subprocess contract, hot loading
  builtins.go      bash read write edit web_* schedule memory session_search skill_read reload notify
  store.go         JSONL transcripts, SQLite for everything else
  http.go ws.go    the only network surface
web/               the PWA
skills/            prose the agent loads on demand
tools/notes/       an example tool, with a schema and a UI panel
data/sessions/<id>/{meta.json,transcript.jsonl}
```

Deleting `data/agent.db` and restarting rebuilds session metadata and the search index from the
transcripts.

## What is not built

- Tailscale Serve and the container image: run it behind either yourself.
- Token counts are estimated at four characters per token, not tokenised.
- Notifications need HTTPS; over plain HTTP the badge and in-app toast still work.
