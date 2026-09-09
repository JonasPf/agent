# Agent — Agent Guide

A single-user autonomous agent in Go: one process, one container, one user. It holds long-lived
conversations, schedules work inside them, and remembers what you tell it. It cannot modify the copy
of itself that is running: a tool reads and writes only its own session's working directory, and the
tool and skill directories are outside it. What it can do is propose a change — a branch, a pull
request, a pipeline — which a person reviews and merges.

The guiding constraint is **no surprises**, in two halves. Nothing happens that the user cannot see:
every scheduled action, memory, and check leaves a visible row or transcript line. And nothing about
the agent changes that the user did not approve: the agent they talk to tomorrow is the one they
merged today. Prototype, not hardened.
See [`specs/index.html`](specs/index.html).

## Build, run, lint, test

Workflows live in [`Taskfile.yml`](Taskfile.yml); run `task` to list them. The
underlying commands are plain `go` and `node` and still work on their
own. `OPENROUTER_API_KEY` must be set to run the agent or the evals (not needed
to build, lint, or test); the agent reads it from `.env`, and `task` loads that
file too.

| Task | Command | Notes |
|------|---------|-------|
| **Build** | `task build` | Compiles all packages and every tool. Agent entry point is `./cmd/agent`; each `tools/<name>` builds to its own `run`. |
| **Run** | `task run` | Serves http://localhost:8080. See the README table for `AGENT_*` env vars. |
| **Lint** | `task lint` | `gofmt -l .` must print nothing, then `go vet ./...`. `task fmt` fixes formatting. |
| **Test** | `task test` | Go tests across the agent and every tool; the browser's pure helpers (`web/transcript.js`) under node's built-in runner. Sub-tasks: `test:go`, `test:web`. `test:go` builds the tools first, because the registry will not load one whose `run` is missing. |
| **Eval** | `task eval -- [tool...]` | Puts each tool's `eval.json` cases to a real model. Costs money; results vary. `AGENT_EVAL_MODEL` overrides the default free model. Not part of the test run. |
| **Reset** | `task db:clear` | Moves sessions, jobs, memory, and per-session files to `.backups/<stamp>`; `task db:restore` puts the newest back. Refuses while the agent is running. |
| **Inspect** | `task db:status` | What the running agent currently holds. |

### Before committing

After every change, and **before committing**, these must all run successfully:

```sh
task check
```

That runs lint, build, and every test suite.

Do not commit if any of them fail. Fix the change (or the tests) until all
of them pass.

**Never skip, disable, or exit tests early to make a run pass.** Do not add
skips, `t.Skip`, `xfail`, `.only`/`.skip`, early returns, commented-out
assertions, or loosened checks to get green. A test must be free to fail when
there is a legitimate reason — a failing test is signal, not an obstacle.
Investigate and fix the underlying cause (in the code or, if the test itself is
genuinely wrong, in the test with a clear reason) rather than suppressing it.

## Every criterion is tested end to end

[R5](specs/index.html) requires each acceptance criterion in the spec to be
covered by a test that drives the system through a real surface — the HTTP API,
the WebSocket, a browser, or a tool run as an actual subprocess. Only the model
provider and the public web may be substituted; everything inside the container
is exercised as shipped.

When you add or change a criterion, add or update the end-to-end test that names
it. A unit test is welcome alongside, but does not discharge the criterion.

## Tools are written the way the agent is

A tool is a Go package in this module at `tools/<name>`, `package main`, developed
test-first, shipping eval cases. `go build ./...`, `go vet ./...`, and
`go test ./...` reach every tool without being told; `task build` compiles each
directory to its own `run`, which is gitignored.

The shared plumbing — the subprocess contract, the API client, the workspace
boundary, the headless browser — is [`internal/tool`](internal/tool). Use it; a
tool that reimplements one of these will disagree with the others.

The registry contract itself is language-agnostic (JSON in on stdin, JSON out on
stdout). This is a rule about how tools are written here, not something the
loader checks. See [`specs/tools.html`](specs/tools.html) and
[ADR-035](specs/adrs.html#adr-035).

**The agent is never rebuilt or restarted for a tool.** Build the tool, call
`reload_tools`.

## Every tool ships eval cases

A tool's `description` and `parameters` are the surface the model programmes
against, and no deterministic test can tell whether they read correctly. So
every directory under `tools/` carries an `eval.json` — requests put to a real
model, and what its tool use must look like. `go test ./...` fails if a tool
directory has none.

When you add a tool, write its eval cases in the same change, and run
`go run ./cmd/eval <tool>`. A failing case is usually a defect in the
description, not in the model; fix it there. See [`specs/tools.html`](specs/tools.html).

## Develop with TDD

Use test-driven development. Before changing behavior, write a test that
captures the intended behavior and watch it fail (red), then make the change to
turn it green, then refactor while keeping it green.

## Keep specs and code in sync

The specification in [`specs/`](specs/index.html) and the code are two views of
the same system and must not diverge. **Every change to behavior must be
reflected in the specs in the same change** — update the relevant spec
document(s) alongside the code. The specs are HTML: `architecture.html`,
`sessions.html`, `jobs.html`, `tools.html`, `skills.html`, `memory.html`,
`webui.html`, `api.html`, and `adrs.html` for decision records.

**The spec has priority unless explicitly stated otherwise.** When the code and
the spec disagree, treat the spec as the source of truth and bring the code into
line with it — unless the task explicitly says the spec is wrong and should be
changed to match new intent. In that case, change the spec first, then make the
code conform.
