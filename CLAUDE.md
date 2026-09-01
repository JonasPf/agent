# Agent — Agent Guide

A single-user autonomous agent in Go: one process, one container, one user. It holds long-lived
conversations, schedules work inside them, remembers what you tell it, and writes its own tools.
The guiding constraint is that nothing happens that the user cannot see — every scheduled action,
memory, and check leaves a visible row or transcript line. Prototype, not hardened.
See [`specs/index.html`](specs/index.html).

## Build, run, lint, test

There is no Makefile or task runner; workflows are plain `go` commands from the repo root.
`OPENROUTER_API_KEY` must be set to run the agent (not needed to build, lint, or test).

| Task | Command | Notes |
|------|---------|-------|
| **Build** | `go build ./...` | Compiles all packages; entry point is `./cmd/agent`. |
| **Run** | `go run ./cmd/agent` | Needs `OPENROUTER_API_KEY`; serves http://localhost:8080. See the README table for `AGENT_*` env vars. |
| **Lint** | `go vet ./...` and `gofmt -l .` | `gofmt -l .` must print nothing; use `gofmt -w` to fix. |
| **Test** | `go test ./...` | Currently `internal/app/projection_test.go`. |

### Before committing

After every change, and **before committing**, these must all run successfully:

```sh
gofmt -l .
go vet ./...
go build ./...
go test ./...
```

Do not commit if any of them fail. Fix the change (or the tests) until all
of them pass.

**Never skip, disable, or exit tests early to make a run pass.** Do not add
skips, `t.Skip`, `xfail`, `.only`/`.skip`, early returns, commented-out
assertions, or loosened checks to get green. A test must be free to fail when
there is a legitimate reason — a failing test is signal, not an obstacle.
Investigate and fix the underlying cause (in the code or, if the test itself is
genuinely wrong, in the test with a clear reason) rather than suppressing it.

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
