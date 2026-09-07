# agent

A single-user autonomous agent. One process, one container, one user.
Implements [specs/](specs/index.html); prototype, not hardened.

## Run

```sh
cp .env.example .env      # then put your key in it
chmod 600 .env
task run
```

Open http://localhost:8080. Add it to the home screen for a full-screen app with its own icon.
Enable notifications on the settings screen to get a banner when the agent says something you
are not reading; it works while the browser is open, and there is no push.

## Tasks

`task` lists every workflow. The commands behind them are plain `go` and `node`, so
nothing here is required.

| | |
| --- | --- |
| `task run` | Serve the agent |
| `task check` | Lint, build, and every test suite — run this before committing |
| `task test` | Tests only (`test:go`, `test:web` individually) |
| `task eval -- schedule` | Put one tool's `eval.json` cases to a real model; omit the name for all |
| `task db:clear` | Move sessions, jobs, memory, and session files to `.backups/<stamp>` and start fresh |
| `task db:restore` | Put the newest backup back |
| `task db:status` | What the running agent holds |
| `task tools:reload` | Reload tools from disk without a restart |

`db:clear` refuses while the agent is running.

## Configuration

Settings come from the environment. At startup the agent also reads `.env` from the working
directory, one `KEY=VALUE` per line, so the key and any settings live somewhere durable instead of in
a shell you have to remember to prepare. Blank lines and `#` comments are skipped, a leading `export`
is tolerated, and a value may be quoted. A variable already set in the environment wins over the file,
so a one-off override still works:

```sh
AGENT_MODEL=anthropic/claude-opus-4.1 task run
```

`.env` holds a credential: keep it mode 600, and out of git. It is already in `.gitignore`, and the
agent warns at startup if other users can read it. Point somewhere else with `AGENT_ENV=path`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPENROUTER_API_KEY` | — | Required for model calls. |
| `AGENT_ENV` | `.env` | File to read settings from. |
| `AGENT_ADDR` | `:8080` | Listen address. |
| `AGENT_MODEL` | `anthropic/claude-sonnet-4.5` | Default model for a new session. |
| `AGENT_DATA` | `data` | SQLite plus one directory per session. |
| `AGENT_WORKSPACE` | `workspace` | Holds one working directory per session, where that session's file and shell tools operate. |
| `AGENT_TOOLS` / `AGENT_SKILLS` | `tools` / `skills` | Scanned at start and on reload. |
| `AGENT_BROWSER` | auto | Chrome-family executable for `web_search` and `web_fetch`. Usual install paths and Playwright's cache are searched when unset. Must sit inside a readable path — see `AGENT_READ_PATHS`. |
| `AGENT_READ_PATHS` | none | Extra directories a tool may **read**, `:`-separated. A tool otherwise reads only the runtime, the tool directory, and its own session's working directory, and writes only the latter. |
| `AGENT_NO_BROWSER` | unset | Set to make `web_fetch` retrieve pages over plain HTTP instead of rendering them. |
| `AGENT_EVAL_MODEL` | `minimax/minimax-m3:free` | Model used by `go run ./cmd/eval`. |
| `AGENT_ROTATE_TOKENS` | `40000` | Projected size at which a session rotates. |
| `AGENT_MEMORY_CAPACITY` | `8000` | Characters of durable memory. |

## Layout

```
Taskfile.yml       every workflow; `task` lists them
cmd/agent          entry point
cmd/eval           puts each tool's eval.json cases to a real model
internal/app
  loop.go          assemble, call, dispatch tools, repeat
  projection.go    transcript → messages (pure; tested)
  sessions.go      prompt entry, availability, summary, rotation, fork
  sched.go         ticks, checks, run log, circuit breaker
  tools.go         manifests, subprocess contract, hot loading
  builtins.go      reload_tools — the only capability that cannot live on disk
  store.go         JSONL transcripts, SQLite for everything else
  files.go         one working directory per session; uploads, listing, copy on fork
  sandbox.go       what a tool may read and write, enforced by the OS
  portable.go      session export and import, as a zip
  http.go ws.go    the only network surface
web/               the PWA
skills/            prose the agent loads on demand
internal/tool      the package every tool is built on (subprocess contract, API, browser)
tools/<name>/      one Go package per capability, each with manifest.json,
                   main.go, eval.json, and a `run` built from it: bash read write
                   edit clock web_fetch web_search schedule memory session_search
                   skill_read notes — written by you, not by the agent
data/sessions/<id>/{meta.json,transcript.jsonl}
```

Deleting `data/agent.db` and restarting rebuilds session metadata and the search index from the
transcripts.

## What is not built

- Tailscale Serve and the container image: run it behind either yourself.
- Token counts are estimated at four characters per token, not tokenised.
