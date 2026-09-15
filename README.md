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

`task run` builds and runs from source, which is the fastest loop and confines nothing off Linux: the
sandbox is Landlock, a kernel facility, and a laptop has no equivalent worth keeping a second policy
for. The agent says so at startup. `task dev` runs the same code in the container it ships in, where a
tool is confined exactly as it is in production. For the same reason `task check` on a Mac skips the
browser and sandbox tests, saying NOT RUN; `task check:container` runs the whole suite on Linux, where
nothing skips. Docker is used where it is installed, Podman otherwise.
Enable notifications on the settings screen to get a banner when the agent says something you
are not reading; it works while the browser is open, and there is no push.

## Tasks

`task` lists every workflow. The commands behind them are plain `go` and `node`, so
nothing here is required.

| | |
| --- | --- |
| `task run` | Serve the agent from source. Off Linux nothing confines a tool, and it says so |
| `task dev` | Serve the agent in its container, the way production runs it, with tools confined |
| `task check` | Lint, build, and every test suite — run this before committing |
| `task test` | Tests only (`test:go`, `test:web` individually) |
| `task check:container` | The same check in the test container (`testenv/Dockerfile`) — Linux, chromium, Landlock — where a test that cannot run fails instead of skipping |
| `task eval -- schedule` | Put one tool's `eval.json` cases to a real model; omit the name for all |
| `task eval:container -- web_browse` | The same evals in the test container, where the browser tools have a browser |
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
agent warns at startup if other users can read it. It lives at the root of `AGENT_STATE`, so moving that
moves the file with it.

| Variable | Default | Meaning |
| --- | --- | --- |
| `OPENROUTER_API_KEY` | — | Required for model calls. |
| `AGENT_ADDR` | `:8080` | Listen address. |
| `AGENT_MODEL` | `anthropic/claude-sonnet-4.5` | Model a new conversation starts on. Each keeps its own for life. |
| `AGENT_STATE` | `.` | What outlives the container: `data/`, `workspace/`, and `.env` beneath it. The one directory a deployment mounts. |
| `AGENT_HOME` | `.` | What the image ships: `tools/`, `skills/`, `web/`, `CHANGELOG.md` beneath it. |
| `AGENT_READ_PATHS` | none | Extra directories a tool may **read**, `:`-separated. A tool otherwise reads only the runtime, the tool directory, and its own session's working directory, and writes only the latter. It only adds; nothing here removes a boundary. |
| `AGENT_REPO` | — | Clone URL of this repository, for the agent to propose changes to itself. Reaches a tool only in a session granted it. |
| `GH_TOKEN` | — | GitHub credential. Reaches a tool only in a session granted it. |

That is the whole list. There were eighteen: seven paths that only ever said
where two roots were, four numbers nothing ever set — two of them defaults for a
per-session setting the interface already edits — and the eval model, which
configures `cmd/eval` and not the agent, so it is now a flag on that command.

Every one of these is in [`.env.example`](.env.example) with its default, and a
test checks that in both directions: a setting missing from that file is one
nobody can find, and one listed there that nothing reads is worse, because
changing it appears to do something.

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
                   edit clock web_fetch web_browse web_search schedule memory session_search
                   skill_read notes — written by you, not by the agent
data/sessions/<id>/{meta.json,transcript.jsonl}
data/db/agent.db   the database and its journals, alone in a directory of their
                   own because Landlock grants a tree or it does not
workspace/<id>/    one working directory per session
```

Deleting `data/db/agent.db` and restarting rebuilds session metadata and the search index from the
transcripts.

## Deploy

One image, one container, one volume. Nothing in this repository names a hosting
platform: the image is a plain OCI image, and anything that runs one runs it.

```
push to a branch → PR → .github/workflows/check.yml runs `task check`
merge to main    → .github/workflows/release.yml builds the image, pushes it to
                   GHCR, and POSTs to $DEPLOY_WEBHOOK if that secret is set
```

The image is built by CI and pulled by tag; the server never compiles anything.
`Dockerfile` is two stages — build the agent, every tool, and a pinned `gh`, then
a Debian runtime with `git` and `gh`, the userland the tools need and nothing
else. The sandbox needs no package: Landlock is the kernel's.

The runtime is Debian because tools are subprocesses: `tools/bash` execs
`/bin/sh`, and the model writes GNU-flavoured shell. `agent -health` is the
container's health check, so the image carries no network client for a request
the agent can make of itself.

### The volume

Everything that outlives the container is under `/app/state`. Mount one volume
there and a redeploy keeps every conversation, job, memory item, and file.

```
/app/state/data       transcripts, the database and its journals, the search index
/app/state/workspace  one working directory per session
```

They are two directories for the agent's convenience, not a boundary. The
boundary is Landlock: a tool is granted its own session's directory and the
directory the database lives in, and is denied everything else — the other
directory included, whether or not it shares a mount. Both are derived from
`AGENT_STATE`, so the layout is one decision rather than two that have to
agree.

### Running it

```sh
docker run -d --name agent \
  -v agent-state:/app/state \
  -e OPENROUTER_API_KEY=sk-... \
  -p 127.0.0.1:8080:8080 \
  ghcr.io/<owner>/agent:latest
```

| Variable | Is |
| --- | --- |
| `OPENROUTER_API_KEY` | Required for model calls. |
| `GH_TOKEN` | Optional. Fine-grained, one repository, contents + pull requests write, no workflow scope — what the agent needs to propose changes to itself. The most it can do with this is open a pull request nobody has merged yet. |
| `AGENT_REPO` | Optional. The repository the agent clones when it changes itself. |

Setting these two in the deployment does not by itself let the agent change
itself. A tool receives a variable only in a session **granted** it by name,
under **Controls → granted environment**, and a grant is fixed for that session's
life. That is deliberate: the credential reaches the one conversation doing the
work, not every conversation forever. `OPENROUTER_API_KEY` can never be granted.

Every other setting has a default; the table under [Configuration](#configuration)
has the rest.

**The agent has no login of its own** — one person, no accounts. It publishes no
host port in production and expects a reverse proxy in front of it terminating
TLS and demanding a credential. Binding to `127.0.0.1` above is the same idea on
a single box. Putting the container on a public port with nothing in front of it
exposes an interface that runs shell commands.

### Deploying to Dokploy

An example, not a dependency — the deployment described here is the one that
runs, and nothing in the repository is shaped around it.

1. Create an **Application**, provider **Docker**, image
   `ghcr.io/<owner>/agent:latest`.
2. **Environment**: `OPENROUTER_API_KEY`, and `GH_TOKEN` / `AGENT_REPO` if the
   agent is to propose changes to itself.
3. **Advanced → Volumes**: one volume mount, `agent-state` → `/app/state`.
4. **Advanced → Resources**: a memory limit. A single-user agent on a shared host
   without one takes the machine down with it; 512 MB is enough.
5. **Advanced → Security**: switch on Basic Auth and set a user and password.
   This is the whole of the access control.
6. **Domains**: the hostname, port 8080, HTTPS on, Let's Encrypt.

Then set `DEPLOY_WEBHOOK` in the repository's GitHub Actions secrets to the
application's deploy webhook URL, so a merge to `main` publishes the image and
the host pulls it.

Secrets live where it is deployed (`OPENROUTER_API_KEY`, `GH_TOKEN`) and in
GitHub Actions (`DEPLOY_WEBHOOK`); none of them are in this repository.

## Changing the agent

The agent proposes changes to itself: it clones this repository into a session's working directory,
edits it there, and opens a pull request that goes through the pipeline above. It cannot change the
copy of itself that is running, and it cannot merge.

There is no code for this. The whole of it is:

| What | Is |
| --- | --- |
| [`skills/changing-yourself.md`](skills/changing-yourself.md) | The procedure, as prose. ~3 KB |
| `git` and `gh` in the image | The two programs it needs. The shell, the file tools and the network are there for every other task |
| `GH_TOKEN`, `AGENT_REPO` | A credential and an address, reaching tools only in a session granted them by name |

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
