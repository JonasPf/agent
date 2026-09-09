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
| `AGENT_REPO` | — | Clone URL of this repository, for the agent to propose changes to itself. |
| `GH_TOKEN` | — | GitHub credential. Reaches only a tool whose manifest names it, and no tool names it yet. |
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

## Deploy

The image is built by CI and pulled by tag; the server never compiles anything.

```
push to a branch → PR → .github/workflows/check.yml runs `task check`
merge to main    → .github/workflows/release.yml builds the image, pushes it to
                   GHCR, and calls Dokploy's deploy webhook
```

| File | Is |
| --- | --- |
| `Dockerfile` | Two stages: build the agent, every tool, and a pinned `gh`, then a Debian runtime with `git` and `gh` — the userland the tools need, and nothing else. The sandbox needs no package: Landlock is the kernel's. |
| `deploy/compose.yml` | The whole deployment: the image to run, named volumes for `data` and `workspace` so a redeploy keeps every conversation, and the Traefik labels that route to it. |

The deployment needs three variables set where it runs, none of which are in this
repository:

| Variable | Is |
| --- | --- |
| `OPENROUTER_API_KEY` | Required for model calls. |
| `AGENT_HOST` | The hostname to serve on. |
| `AGENT_BASIC_AUTH` | `user:bcrypt-hash`, **with every `$` doubled**. The agent has no login of its own, so this is the whole of the access control. A hash in a public repository is a password with a cost factor in front of it, which is why it is set here rather than committed. |

Generate that value with the doubling already applied:

```sh
htpasswd -nbBC 12 <user> '<password>' | sed -e 's/\$/$$/g'
```

The doubling is not optional and its absence is not obvious. Compose reads `$name`
inside a substituted value as another variable, so a single-`$` bcrypt hash arrives
at Traefik truncated at its first field — `jonas:$2y$05` and nothing more. Traefik
accepts that as a perfectly valid user list which no password will ever match, and
the failure presents as a password that does not work.

The runtime is Debian because tools are subprocesses: `tools/bash` execs `/bin/sh`, and the model
writes GNU-flavoured shell. `agent -health` is the container's health check, so the image carries no
network client for a request the agent can make of itself.

Secrets live in Dokploy (`OPENROUTER_API_KEY`, `GH_TOKEN`) and in GitHub Actions
(`DOKPLOY_DEPLOY_WEBHOOK`); none of them are in this repository.

## Changing the agent

The agent proposes changes to itself: it clones this repository into a session's working directory,
edits it there, and opens a pull request that goes through the pipeline above. It cannot change the
copy of itself that is running, and it cannot merge.

There is no code for this. The whole of it is:

| What | Is |
| --- | --- |
| [`skills/changing-yourself.md`](skills/changing-yourself.md) | The procedure, as prose. ~3 KB |
| `git` and `gh` in the image | The two programs it needs. The shell, the file tools and the network are there for every other task |
| `GH_TOKEN`, `AGENT_REPO` | A credential and an address, reaching only a tool whose manifest names them |

No tool, no API route, no branch in the loop — nothing in any package knows this capability exists,
and a test fails if that changes. Deleting the skill and the two packages deletes the capability.

**Nothing in the agent enforces the procedure.** A skill is advice to a model that already has a
shell. The controls are all outside: the credential's scope, branch protection, a pull-request
pipeline with no secrets, and the sandbox that keeps a tool out of the running agent's directories.
Where the sandbox does not run, the pipeline is not the only way in.
See [ADR-036](specs/adrs.html#adr-036) and [Changing the agent](specs/architecture.html).

## What is not built

- Tailscale Serve: the deployment uses a password in front of a public hostname instead — [ADR-038](specs/adrs.html#adr-038).
- The tool sandbox runs where the host permits unprivileged user namespaces. Where it does not, the interface says `NOT ENFORCED` rather than implying a boundary that is not there.
- Token counts are estimated at four characters per token, not tokenised.
