# Plans and handover

Three plans for work in progress, written so that a different model (the intended reader is
Claude Opus) can pick up at any point with no memory of the conversations that produced them.
**Keep them current: a pull request that finishes or changes a step updates the plan in the same
pull request.** A plan that lags the code is worse than none.

| Plan | What it is | Status |
|---|---|---|
| [`01-agent-core.md`](01-agent-core.md) | Shrink the core: tools and skills become installable plugins (MCP bundles), living in their own repository | Steps A–B done (B is on `main` only once PR #91 merges). Next: C |
| [`02-voice-over-bundle.md`](02-voice-over-bundle.md) | Turn the voice-over skill into an MCP bundle that also runs in Claude Code, Codex, OpenCode; cloud and local speech backends | Not started. Depends on 01 steps E and F |
| [`03-interface.md`](03-interface.md) | Redesign the operator interface ("more slick") | Not started. Independent of 01 and 02 until 01 step G |

Update the Status column and each plan's own "Status" section as work lands.

## Read first

1. [`../AGENTS.md`](../AGENTS.md) — the repository's rules. They override defaults. The ones that
   bite: test-first; a behaviour change updates `specs/` and `CHANGELOG.md` in the same commit;
   every acceptance criterion has an end-to-end test (R5); `task check` must pass before a commit;
   never skip or loosen a test to get green.
2. [`../specs/adrs.html`](../specs/adrs.html) ADR-085 and ADR-086 — the two decisions plan 01
   carries out. The next free ADR number is **087**.
3. The plan you are working on, top to bottom, before touching code.

## Who decides what

Jonas, the operator and only user, has said the codebase is the model's to design: what it is for
and how it is built are your call. Decide, write the reason into an ADR, and proceed. Ask him only
for:

- anything outward-facing or hard to undo: creating a repository, publishing a release, deleting
  data, force-pushing;
- taste (plan 03: he picks the visual direction from mocks);
- spending money beyond a few cents of evals.

He merges pull requests; you open them. He has limited time: lead a message with what he should
do next, keep it short, number the steps.

When you propose a fix for a failure, say how often the failure happens and what the fix adds to
every model call in tokens. He declines fixes for one-off edge cases (he turned down an atomic
write of `meta.json` for that reason) because the agent runs very long conversations on a cheap
model.

## How to work in this repository

- **The code only compiles on Linux.** `internal/app` binds Landlock and btrfs directly. On the
  Mac, `go test ./...` is a compile error, and editor diagnostics about `unix.Renameat2`,
  `syscall.SYS_PRCTL`, `unix.Syncfs` are that, not bugs.
- **Fast feedback on the Mac:** `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go vet ./...` type-checks
  without a container. `gofmt -l .` must print nothing.
- **Chosen tests, in the container:** `task test:run RUN='TestOne|TestTwo'` (add
  `PKG=./cmd/eval` for another package; the default is `./internal/app`). It builds the tools
  first, because the registry refuses a tool whose `run` is missing.
- **The gate:** `task check` (about four minutes). It is what CI runs.
- **The shell is zsh.** An unquoted glob that matches nothing aborts the whole command line
  (`--include=*.go` did). Quote globs.
- **One pull request per step, each from a fresh branch off `main`.** Do not stack a pull request
  on another's branch: #90 was stacked on #89, was merged into #89's branch after #89 had gone to
  `main`, and its commits never reached `main` (PR #91 repairs that). If a step truly needs an
  unmerged one, wait, or say so and let Jonas merge in order.
- **Commit messages** end with your own `Co-Authored-By:` line; pull request bodies say what
  changed, what was left out, and how it was tested.
- **Tests that need a model** use the stubs in `internal/app/*_test.go`: `scriptedModel` (a turn
  that calls tools step by step), `modelBackedApp`, `fakeModel`. `confinedApp` gives an agent
  under the real sandbox; `callAPI` drives the HTTP API; `settle` waits for a turn to end.
- **Test MCP server:** `testenv/mcp/shell` (one tool, `sh`). `internal/app/bundles_test.go` shows
  how a test builds it into a bundle folder and loads it.

## Facts established so far (do not re-derive)

- **MCPB** (MCP Bundles, formerly DXT): a zip with `manifest.json` (spec 0.3) and a local MCP
  server. `server.type` may be `binary`. `user_config` entries may be `sensitive`. `_meta` is for
  what the format has no field for; this agent's key under it is `io.github.jonaspf.agent`
  (`reads`, `timeout_seconds`). No Go library exists; `internal/app/bundles.go` reads the manifest.
- **Go SDK:** `github.com/modelcontextprotocol/go-sdk` v1.7.0, protocol revision `2026-07-28`.
  Client: `mcp.NewClient` + `mcp.CommandTransport{Command: *exec.Cmd}`; `ClientSession.Tools`,
  `.CallTool`. Server: `mcp.NewServer`, `mcp.AddTool`, `server.Run(ctx, &mcp.StdioTransport{})`.
- **One server process per tool call** costs 5.1 ms against 4.2 ms for the old `run` (20 calls
  each, under Landlock). So no long-lived tool processes; a bundle's server must keep its state in
  files in the working directory.
- **ext-skills** (`io.modelcontextprotocol/skills`, SEP-2640, Beta): skills served by a running
  server via `skills/list` and `resources/read`. Decided: not the base mechanism. Skills install as
  Agent Skills folders. It may be added later so a bundle can bring skills with its tools.
- **Elicitation:** URL mode is for data that must *not* reach the client (OAuth, payment); it
  needs a page the user's browser can reach, which a sandboxed per-call server here cannot serve.
  Form mode is a flat form and maps onto `ask_operator`'s `questions`. Neither replaces
  `ask_operator`, because elicitation is a server asking, not the model asking.
- **MCP Apps** (`modelcontextprotocol/ext-apps`, spec 2026-01-26 marked stable, SEP-1865): a tool
  declares a `ui://` resource; the host shows it in a sandboxed frame; the page calls the server's
  tools through the host. Listed hosts: ChatGPT, Claude, VS Code, Goose and others. Claude Code,
  Codex CLI and OpenCode were not listed. Only the repository's front page was read; read the
  spec itself before building on it.
- **Security model that must survive every change:** a tool runs confined to its conversation's
  directory (Landlock), with an allow-listed environment, never the model key
  (`OPENROUTER_API_KEY`); the agent cannot modify itself (tool, skill and plugin directories are
  outside every writable path); only the operator installs things; requests from other sites are
  refused (`internal/app/origin.go`, ADR-084); a page from a working directory is served under a
  sandbox policy with no network (`filePolicy` in `internal/app/http.go`).

## Small known issues, left on purpose

- `POST /sessions` with a `from` that names no session starts a plain session silently. Check
  where `web/app.js` sends `from` before changing it.
- `internal/app/builtins.go` `packagesTool` and the old handler it came from assign into
  `x.Packages[i]`, which shares its backing array with the published session. Harmless so far.
- Queue goroutines (`App.queues`) are never removed when a session is deleted: one idle goroutine
  per session used since start.
