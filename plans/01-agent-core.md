# Plan 01: a small core, with tools and skills as plugins

## Status

| Step | What | State |
|---|---|---|
| A | Spike and decisions (ADR-085, ADR-086) | Done: PR #88 |
| B | Core hosts MCP bundles; five tools built in | Done: PR #89 on `main`; PR #90's commits are on `main` only once **PR #91** merges |
| C | `ask_operator` built in; generic tool panels removed | Not started |
| D | Tool API removed | Not started |
| E | `agent-plugins` repository: eight tools as bundles, five skills, releases | Not started. **Needs Jonas's go-ahead to create the repository** (he was asked; proposed name `JonasPf/agent-plugins`, public) |
| F | Install surface: plugins API, bundle configuration, skill install by upload or URL | Not started |
| G | Plugins screen in the interface | Not started |
| H | Image takes its default set from `plugins.lock`; `tools/`, `skills/`, `internal/tool` leave the core | Not started |
| I | MCP Apps hosting in the panel | Not started. Research first |
| J | Optional: form-mode elicitation; ext-skills | Not planned in detail |

Before starting any step: `git checkout main && git pull`, and check that
`internal/app/builtin/` exists. If it does not, PR #91 is not merged yet; ask Jonas to merge it.

## Goal

The core holds only what makes it an agent: the turn loop, the store, sessions, the scheduler,
the sandbox, the HTTP API and interface, an MCP host, and the tools that are the agent's handle
on itself. Every other tool, and every skill, is installed like a plugin and lives in another
repository. Adding or changing a tool no longer needs a pull request to the agent.

"No surprises" still holds: only the operator installs; what the image ships is pinned in a merged
file; the agent cannot install or change a plugin.

## Where things are now

- `internal/app/bundles.go` — reads an unpacked bundle, asks its server for tools, runs one
  confined server per call. `Registry.bundleDirs` = `[home/plugins, state/plugins]`
  (`Config.PluginsDir`, `Config.UserPluginsDir` in `internal/app/app.go`).
- `internal/app/builtins.go` — `schedule`, `session_search`, `skill_read`, `call_model`,
  `packages`, `reload_tools`. Manifests and eval cases are embedded from
  `internal/app/builtin/<name>/{manifest.json,eval.json}`. `builtinRuns()` maps a name to its code;
  a test fails if a manifest has no code or the reverse.
- `internal/app/tools.go` — the registry, and the old loader for a tool with a `run` of its own
  (`tools/<name>/manifest.json` with an `agent` block). Still used by the nine tools left in
  `tools/`: `ask_operator`, `bash`, `clock`, `edit`, `read`, `web_browse`, `web_fetch`,
  `web_search`, `write`.
- `internal/app/toolapi.go` — the tool API, down to `GET /skills/{name}`, which only
  `tools/read` calls (to tell the model "this path is a file of skill X, use skill_read").
- `skills/` — five shipped skills: `keeping-records.md`, `researching.md`, `video-edit/`,
  `voice-over/`, `writing-skills/`. Their script tests are `testenv/skills/*`.
- What a bundle may not do yet: need a `user_config` value (refused at load), be anything but a
  `binary` server, return non-text content (named in the result, not passed on).

---

## Step C — `ask_operator` built in; generic tool panels removed

**Why:** it is the last tool that is the agent's handle on something only the agent has (the
operator). It is also the only tool with a panel, so the generic panel mechanism can go.

**What exists:** `tools/ask_operator/main.go` (193 lines) validates the ask and writes
`asks/<id>.json` into the working directory, returning a link
`#toolpanel/ask_operator/<session>/<id>`. `tools/ask_operator/ui/panel.js` (260 lines) is loaded
by `web/app.js` with `import('/tools/<name>/panel.js')` and drawn by `viewToolPanel`. The panel
reads the ask through the files API, saves files through `POST /tools/ask_operator/call`, and
sends the answer as an ordinary message. Spec: `specs/tool-ask_operator.html`, ADR-073, ADR-074.
Browser tests: `web/ask_operator.test.js`, `internal/app/ask_operator_test.go`.

**Do:**

1. Move `manifest.json` (without its `agent` block) and `eval.json` to
   `internal/app/builtin/ask_operator/`. Port `main.go` to `askOperatorTool` in `builtins.go`
   (or a file of its own) and add it to `builtinRuns()`.
2. **Trap:** a builtin is not sandboxed. Everything it reads or writes in the working directory
   must go through `App.workspaceRoot` (an `os.Root`) or `writeWorkspaceFile`
   (`internal/app/files.go`), never `filepath.Join` on a path the model supplied. The old tool was
   protected by Landlock; the builtin is protected only by this.
3. Move `panel.js` to `web/` and have `viewToolPanel` load it from there. **Keep the link format**
   `#toolpanel/ask_operator/<session>/<id>`: old transcripts hold these links.
4. Remove the generic mechanism: `GET /tools/{name}/panel.js` (`hPanel`), `Tool.HasPanel`,
   `has_panel` in `GET /tools`, the "Panels" list in `web/app.js`, and their mentions in
   `specs/webui.html` (section "Panels"), `specs/tools.html` (section "Interface plugins"),
   `specs/mock-tools.html`, `specs/openapi.yaml`. Keep `POST /tools/{name}/call` (the panel uses
   it to save files; it now reaches the builtin).
5. Delete `tools/ask_operator/`.

**Tests first:** existing `ask_operator_test.go` cases should pass unchanged against the builtin;
add one that a `page` reached through a symlink out of the working directory is refused.
Update `TestOnlyTheAgentsOwnHandlesAreCompiledIn` (`job_transparency_test.go`).

**Done when:** `task check` passes; an ask made in `task dev` opens its panel, and Send delivers
the answer; ADR-086's meta line no longer says `ask_operator` follows.

---

## Step D — remove the tool API

**Why:** after step C nothing but `tools/read` uses it. Removing it deletes a listener, per-call
tokens, and a port in the sandbox policy.

**Do:**

1. Give tools the one fact `read` asked for another way. For every tool call (old kind and
   bundle), set `AGENT_SKILL_DIRS` to a JSON object of skill name to folder, for the folder skills
   the calling conversation has (`App.skillFolders` in `internal/app/skill_install.go` already
   computes the folders; names come from the same loop). Change `tools/read/main.go` to read it
   instead of calling `tool.TryAPI`. Add the name to `toolContractEnv` (`internal/app/env.go`) so
   no session variable can stand in for it.
2. Delete: `internal/app/toolapi.go`; `App.calls`, `App.toolAddr`, `listenTools`, `toolURL`
   (`app.go`, `Run`); `AGENT_URL` and `AGENT_TOKEN` in `tools.go` and `env.go`;
   `Sandbox.SetToolAPIPort` and the port in the policy, in `Describe()` and in `Reach()`
   (`sandbox.go`, `landlock.go`); `API`, `TryAPI`, `APIError`, `SetAPITimeout` in
   `internal/tool/tool.go`.
3. Tests: delete `toolapi_test.go`'s API tests (keep the builtin ones, move them to a
   `builtins_test.go`); remove `listenTools` calls from test helpers (`metrics_test.go`,
   `search_read_test.go`, `search_words_test.go`); add a sandbox test that a confined tool cannot
   connect to any loopback port of the agent (there is a `-dial` helper in `main_test.go`).
4. Specs: `architecture.html` (the paragraph "A tool reaches the agent on an API of its own", the
   port table row, two acceptance criteria), `tools.html` (environment table), `mock-tools.html`
   (the "tool API" port chip), ADR-051 marked superseded by ADR-086, ADR-086 marked accepted.

**Done when:** `grep -rn "AGENT_URL\|toolRoutes\|callTokens" internal tools` finds nothing and
`task check` passes.

---

## Step E — the plugins repository

**Needs Jonas's yes before `gh repo create`.**

**Layout** (one Go module, `github.com/JonasPf/agent-plugins`):

```
bundles/<bundle>/manifest.json     MCPB 0.3, server.type "binary"
bundles/<bundle>/main.go           the MCP server (go-sdk), one or more tools
bundles/<bundle>/*_test.go
bundles/<bundle>/evals/<tool>.json the eval cases, in the agent's existing format
internal/...                       what the tools share (from the agent's internal/tool)
skills/<name>/SKILL.md ...         the five skills, unchanged
testenv/                           Dockerfile and the skills' script tests (from the agent)
.github/workflows/                 check on push; release on tag
```

**Bundles** — grouped by what they are allowed, since a bundle is the unit of permission:

| Bundle | Tools (names unchanged) | Needs |
|---|---|---|
| `files` | `read`, `write`, `edit` | nothing |
| `shell` | `bash` | `_meta` timeout ≥ the largest `timeout_seconds` the tool accepts |
| `clock` | `clock` | nothing |
| `web-fetch` | `web_fetch` | network |
| `web-search` | `web_search` | `user_config.tavily_api_key` (sensitive), `tavily_url` (optional) |
| `web-browse` | `web_browse` | `_meta.reads` `/proc`, `/sys`, `/var`; a browser in the host image |

**Rules for a port:**

- Tool names, descriptions and parameter schemas stay **exactly** as they are (copy the manifest's
  `description` and `parameters` into the tool definition; do not let the SDK infer a schema).
  Existing conversations name tools, and the eval cases test these descriptions.
- A server keeps no state between calls except files in the working directory; the agent starts
  it again for every call.
- The working directory is the process's current directory. Do not rely on `AGENT_*` variables;
  other hosts do not set them. (`AGENT_SKILL_DIRS` is the one exception `read` may use if set.)
- Success is text content. Failure is `isError: true` with the message as text.
- `bash` must keep what `tools/bash/main.go` does today: its own process group, kill the group on
  timeout, `WaitDelay` so a background process holding the pipe does not hang the call, and a
  background process that exits is reaped. `internal/app/tool_stalls_test.go` in the agent
  describes these three behaviours; port the tests.
- Move each tool's Go tests and eval cases with it. Sandbox tests stay in the agent (they test
  the agent's sandbox, with the stand-in server).

**Releases:** on a tag `<bundle>-v<semver>`, build `linux/amd64`, `linux/arm64`, `darwin/arm64`
(`CGO_ENABLED=0`), zip each as `<bundle>-<version>-<os>-<arch>.mcpb` with
`compatibility.platforms` set, attach to a GitHub release with a `sha256sums.txt`. Skills are
released as one `skills-<version>.tar.gz`.

**Done when:** every bundle's `.mcpb`, unpacked by hand into `.dev`'s `plugins/` folder in a
`task dev` agent that has the old tool removed from `tools/`, loads and passes a manual call; the
repository's CI is green; a first release exists. The agent repository is **not changed** in this
step.

---

## Step F — install surface (API)

**Decisions:**

- Bundles are installed into `state/plugins/<name>/` (already read by the loader). Configuration
  values live in `data/plugins.json`, mode 0600, like `push.json` (`internal/app/push.go`) — never
  in the database, a transcript, or an API response. The API says only whether a value is set.
- `${user_config.KEY}` is filled into `mcp_config` `args` and `env` at call time
  (`readBundle` in `bundles.go` currently refuses any `${…}` but `${__dirname}`; replace that
  with: unknown variable → refused; known but unset and required → the bundle is listed as
  "needs configuration" and its tools are not offered).
- Installing is two requests, so the operator sees what a bundle asks for first.

**Routes** (each needs an entry in `specs/openapi.yaml`; `api_documented_test.go` fails otherwise):

| Route | Does |
|---|---|
| `GET /plugins` | Every bundle: name, version, description, where from (`image` / `installed`), tools, config keys with `set: true/false`, `reads`, timeout, load failure if any |
| `POST /plugins/inspect` | Body: an uploaded `.mcpb`, or `{"url": "https://…"}`. Unpacks to a temporary folder, reads the manifest, runs the server once (sandboxed) to list tools. Answers what `GET /plugins` would show, plus the archive's sha256. Installs nothing |
| `POST /plugins` | Same body plus the sha256 from inspect. Installs, replacing a version already installed, and reloads tools |
| `PUT /plugins/{name}/config` | `{key: value}`; `null` clears. Reloads |
| `DELETE /plugins/{name}` | Removes an installed bundle and its configuration. An image-shipped bundle cannot be deleted |
| `POST /skills/install` | Already takes a draft from a working directory. Add: an uploaded zip, or `{"url": …}` to a zip or tar.gz of one Agent Skills folder |

**Safety, each with a test:**

- Unpack with an `os.Root` on the target folder; refuse symlinks, absolute paths, `..`; cap total
  and per-file size (start with 300 MB and 200 MB); keep the executable bit from the zip.
  `internal/app/portable.go` (session import) is the pattern.
- Unpack beside the target and rename into place, so a failed install leaves the old version.
- URL installs: `https` only, size-capped, fetched by the agent process. This is an operator-only
  route; the cross-site guard (`origin.go`) already covers it.
- No tool, builtin or bundle, can install: there is deliberately no tool for it, and
  `state/plugins` is outside every sandbox-writable path (a test in `bundles_test.go` already
  asserts the second).
- A bundle that replaces a builtin's or another bundle's tool name is refused (exists).

**ADR-087:** how a bundle is installed and configured (the decisions above).

**Done when:** the stand-in bundle (`testenv/mcp/shell`, zipped in a test) installs, configures,
calls, and uninstalls through the HTTP API in an end-to-end test; `task check` passes.

---

## Step G — Plugins screen

The Tools screen becomes Plugins: built-ins, then each bundle with its tools, where it came from,
what it may read, its timeout, and a form for its configuration; an Install button (file or URL)
that shows the inspect result and asks before installing; Remove for installed bundles.
Follow plan 03's design if it has landed; otherwise match the current screens.
Spec: `specs/webui.html` (new acceptance criteria), `specs/mock-tools.html`. Tests:
`web/shell.test.js` has the pattern for screen tests.

---

## Step H — the image takes its tools and skills from releases

1. `plugins.lock` (JSON, checked in): for each bundle and for the skills archive — name, version,
   and per architecture a URL and sha256. The `Dockerfile` build stage downloads the entry for
   `TARGETARCH`, verifies the digest, and unpacks to `/out/plugins/<name>` and `/out/skills`
   (the `gh` and `glab` steps in the `Dockerfile` are the pattern); the final stage copies them to
   `/app/plugins` and `/app/skills`. A digest that does not match fails the build.
2. `web_search`'s key: on start, if `TAVILY_API_KEY` is in the agent's environment and the
   `web-search` bundle has no stored value, store it (one-time adoption, logged), so the deploy
   does not silently break search. `Store.adoptGrants` is the precedent for adopting on start.
3. Switch the core's tests from `../../tools` to the stand-in server (12 test files use
   `copyToolTree` or `"../../tools"`; extend `testenv/mcp/shell` only as far as the core's own
   tests need). Tests that test a tool's own behaviour were moved in step E; delete them here.
4. Delete `tools/`, `skills/`, `internal/tool/`, `testenv/skills/`, the old loader in
   `tools.go` (`loadToolDir`, `strayField`, `applySchema`, `ToolAgent.DBPrefix`, `AGENT_DB*`),
   the tools loop in the `Dockerfile`, `build:tools` and `test:skills` in `Taskfile.yml`.
5. Evals: `LoadEvalSuites` reads `evals/<tool>.json` from each loaded bundle beside the embedded
   builtin suites. `task eval -- bash` must still work in the test container, which now needs the
   bundles: add a `plugins:fetch` task that fills a local folder from `plugins.lock`.
6. Rewrite `AGENTS.md` sections "Tools are written the way the agent is" and "Every tool ships
   eval cases", `specs/tools.html` (Layout, Manifest, Execution, Loading, Who writes a tool),
   `README.md`. Mark ADR-035 and ADR-067 superseded by ADR-085.

**Done when:** `task dev` serves an agent whose `bash`, `read`, `web_fetch` come from bundles and
work in a turn; `task check` and `task eval -- bash` pass; the deployed agent's existing
conversations still run (tool names unchanged).

**Cost to say in the changelog:** existing conversations pay one uncached prompt after the
deploy, because the order of tools sent changes.

---

## Step I — MCP Apps hosting

**Research before designing.** Read the specification in `modelcontextprotocol/ext-apps`
(spec version 2026-01-26 and the draft), not summaries. Find out: how a tool names its `ui://`
resource; the exact messages between page and host; what the host must enforce (sandbox, CSP);
whether a page can send a message into the conversation.

**Outline to verify against the spec:** the panel (`viewToolPanel` in `web/app.js`) becomes a
host. The agent reads the bundle's page with `resources/read` (one confined server process, as
for a call) and serves it under `filePolicy` (no network, no origin). The panel relays the page's
tool calls to a new operator route that runs the bundle's tool in the conversation's directory.
A call the operator's page makes is the operator's act; whether its result is also written to
the transcript is a decision for the ADR. `ask_operator` stays, for pages the model writes.

Needs its own ADR. Plan 02 uses this for the voice-over review page.

---

## Step J — optional, only on need

- **Form-mode elicitation:** a bundle's tool answers "input required" with a flat form; the
  agent shows it like `ask_operator`'s questions and retries the call with the answers. Check what
  go-sdk v1.7.0 exposes for `InputRequiredResult` first.
- **ext-skills client:** only so a bundle can bring skills with its tools. The host must verify
  digests, cache files read-only on disk (scripts run from there), and ask before a served skill
  can cause code to run.
