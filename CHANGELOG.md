# Changelog

## 2026-09-15

- The model is picked in a dialog that slides in over the screen. Each model
  shows its price per million tokens in and out, its context window, and its
  Artificial Analysis intelligence, coding, and agentic scores where OpenRouter
  reports them, with a link to its OpenRouter page. The list can be searched and
  sorted by smartest, cheapest, largest context, or newest.
- A new conversation starts on the model you last picked, for a new
  conversation or for a fork onto a different model, and still does after a
  restart. `AGENT_MODEL` is gone: nothing reads it any more.
- Granted environment is a button. It opens a list of every variable name the
  agent could grant, the ones from `.env` first, each with a checkbox. Values
  are never shown, and the model key is never listed.
- A skill can ship switched off with `default: off` in its frontmatter, and
  `changing-yourself` now does. Tick it under skills for a conversation that is
  meant to change the agent. A conversation already running with the default
  skills loses it at its next compaction, when its prompt is rewritten.

- `web_browse` and `web_search` introduce themselves as an ordinary Chrome of
  the installed version, without the "HeadlessChrome" that headless chromium
  announces by default. Sites that turn automation away by that one word now
  serve the page. Checks that test the browser itself, like Cloudflare's
  "Just a moment…", still stop it; nothing answers or evades them.
- When a page is a bot check, `web_browse` fails and says whose it is —
  Cloudflare, DataDome, HUMAN — instead of returning the check's text as though
  it were the page.
- `bash` tells the model what its sandbox refuses: only the workspace is
  writable, so scratch files go under `$TMPDIR`, and a browser will not start
  from the shell. Web pages are for `web_fetch` and `web_browse`.
- Eighteen settings become eight. Seven paths only ever said where two roots
  were, so there are now two: `AGENT_STATE` for what outlives the container and
  `AGENT_HOME` for what the image ships. Four numbers that nothing ever set are
  constants — two of them were defaults for `compact_at_tokens` and
  `keep_verbatim_tokens`, which a session already carries and the interface
  already edits. And the eval model configures `cmd/eval` rather than the agent,
  so it is a flag on that command: `go run ./cmd/eval -model <id>`.
- `.env.example` lists every setting the agent reads, with its default, and a
  test keeps it that way in both directions. Thirteen were missing, including
  `AGENT_EVAL_MODEL`; the README documented `AGENT_ROTATE_TOKENS`, which nothing
  has read since compaction replaced rotation.
- The eval model is `google/gemma-4-26b-a4b-it`. The free tier it replaces was
  withdrawn, and a withdrawn model failed every case at once in a way that read
  as broken cases — so a run now checks the gateway lists the model first and
  says so if it does not. The new one is cheap rather than free, and takes under
  two seconds a case where the last free one took three and a half minutes.
- `web_fetch` is back, as its own tool: one HTTP request, no browser, for an
  API, a raw file, a feed, a README — most of what is actually asked for, in
  milliseconds instead of seconds. It is the first thing to reach for, and
  `web_browse` is for pages that need scripts.
- When a fetched page turns out to be assembled by its scripts, `web_fetch` says
  so and names `web_browse`. That is the difference from the old fallback it
  replaces: the substitution is offered, not made, so a shell can never be
  mistaken for a page that simply says little.
- A conversation grants its tools the environment they may read. `granted_env`
  on a session names the variables its tools receive — names, never values — and
  is fixed for that session's life like the model and the tools beside it. A
  credential now reaches the one conversation doing the work rather than every
  conversation there will ever be, which is what a tool manifest could only say.
  Set it under **Controls → granted environment**.
- `OPENROUTER_API_KEY` cannot be granted to anything, ever.
- The `changing-yourself` skill checks for `AGENT_REPO` and `GH_TOKEN` before it
  starts, and says which is missing instead of failing halfway through a change
  it cannot push.
- `web_fetch` is now `web_browse`, which is what it does. It opens the page in a
  browser, every time; the plain-HTTP fallback is gone, because a page that
  needed its scripts came back looking exactly like a page that simply says
  little, and whoever asked could not tell which they had.
- The browser is installed in the image at one path instead of searched for.
  `AGENT_BROWSER` and `PLAYWRIGHT_BROWSERS_PATH` are gone with the search.
- Fixes `web_browse` failing on every call in production, where it was meant to
  fall back and could not.

## 2026-09-14

- The version screen shows what changed and nothing else. The changelog's note
  to whoever writes it is a rule for this repository, kept in `CLAUDE.md`, and
  the screen is served the dated entries from the first one on.
- The repository ships an image, not a deployment. The Compose file is gone and
  nothing in the code, the workflows, or the image names a hosting platform; how
  to run it on one is documentation, with Dokploy worked through as an example.
- One volume. Everything that outlives the container is under `/app/state`, and
  the two directories inside it are the agent's own convenience rather than a
  boundary — Landlock is the boundary, and a tool confined to one session is now
  shown to reach nothing else under a shared root.
- `task db:clear` backs the database up again. It had been looking for it beside
  the transcripts, where it stopped living when Landlock gave it a directory of
  its own, and had been quietly leaving it behind.

## 2026-09-11

- Compact a conversation in place: a compaction rewrites the session it happens
  in rather than seeding a successor, so a conversation keeps one identity for
  its whole life.
- `bash` says when it truncated its output instead of handing the model a body
  cut without notice.
- Every tool has a page of its own in the spec, saying what its manifest cannot.
- One sandbox mechanism: Landlock, with no second policy to keep in step.
- The interface warns when nothing is enforcing the sandbox, and the browser
  tools use whatever browser is actually installed.

## 2026-09-09

- A tool asks for the paths it needs, and the environment is hidden behind
  `/proc`.
- The sandbox grants `/sys` and `/var` as well: a runtime is more than its
  interpreter.
- Tools are confined with Landlock, which works in an unprivileged container
  where bubblewrap could not.
- The principle is written down: no surprises, in two halves. Changing the agent
  is composition, not a feature.

## 2026-09-08

- The deployment refuses to start without the credential in front of the agent,
  and routes from the compose file rather than a file placed by hand.
- A sandbox is claimed only after it has been proven to run.
- Nothing personal is published: fixtures are anonymised and the operator's
  notes are untracked and out of the build context.

## 2026-09-07

- The agent deploys as an image built by CI and proposes changes to itself
  through pull requests.
- A tool is given only the environment its manifest names.
- The interface is one responsive shell at every width.
- Finished jobs can be deleted in one action.
- The browser tools run inside the sandbox and no longer phone home while
  fetching a page.
- The agent is rebuilt around a tool subprocess contract, per-job pricing, and a
  task-driven workflow.

## 2026-09-01

- First working agent: sessions, jobs, memory, tools, and the web interface.
