# Changelog

## 2026-09-17

- The image now carries `glab`, GitLab's command-line client, beside `gh`. A
  conversation can work on a GitLab project the way it already works on a GitHub
  one, and `GITLAB_TOKEN` joins `GH_TOKEN` among the credentials you can grant a
  conversation under **Controls → granted environment**.
- A new skill, **working on a repository**, says how to do that on anyone's
  repository: where the credential comes from, why `gh auth login` and ssh keys
  cannot work in here, how to clone over HTTPS instead, and that a pushed branch
  is a proposal rather than a finished change. Like *changing yourself*, it stays
  out of a conversation that did not ask for it — tick it under **Controls** when
  you set out to use it.

## 2026-09-16

- A tool call in a conversation now carries a **logs** toggle when the tool
  wrote anything to standard error. It stays closed, including on a call that
  failed and is already showing its error, and what it holds travels with the
  conversation when you copy it. These logs were always kept; there was simply
  nowhere to read them.
- When `web_fetch` cannot read a page — a 404, a refusal, a connection that
  never answered — it now names `web_browse` as the next thing to try, and says
  to look elsewhere rather than fetch the same URL again if that fails too. It
  used to say this only about pages it had already fetched successfully.
- `web_fetch` also recognises a page that never rendered by the template syntax
  left in its text — a binding nothing replaced, an attribute expression that
  leaked into the page — and not only by how little text there is. A shell with
  a large navigation bar used to pass as a page that simply says little.

## 2026-09-15

- On the Tools screen, each tool's sandbox is split in two: **Every tool**,
  which is the same on every card, and **This tool only**, which lists the
  paths that tool's manifest asked for (the browser tools ask for `/proc`,
  `/sys`, and `/var`).
- While the agent is working, the status line under the message box says so
  and counts the seconds, starting the moment you send. Reopening the
  conversation mid-turn keeps the count going from when the turn started.
- **Copy** in a conversation's header puts the whole conversation on the
  clipboard as Markdown: every message, each tool call and its result (long
  results clipped), and compaction summaries.
- The shell has `curl`, `wget`, `python3`, and `perl`.

- **The interface moves from port 8080 to 7770.** A proxy or port mapping
  that points at 8080 must point at 7770, or the interface will not answer.
  8080 is left free for the development servers tools start.
- Tools can no longer reach the interface's own API. They reach the agent
  through an API of their own, which offers memory, their own conversation's
  jobs, the skills that conversation has, and search. Nothing on it creates,
  forks, or messages a conversation, so a tool can no longer start a session
  holding `GH_TOKEN` or schedule a job into one.
- Inside the container, a tool may open TCP connections only to common ports:
  22, 53, 80, 443, 3000, 3306, 4000, 5000, 5173, 5432, 6379, 8000, 8080, 8443,
  8888, 9418, and 27017. Add others with `AGENT_TOOL_PORTS`. Linux older than
  6.7 cannot enforce this, and the agent says so.
- The Tools screen shows, for each tool, which paths it may read and write and
  which ports it may connect to, and says when either is not enforced.
- A job's check command no longer sees the agent's whole environment. It gets
  what a `bash` call in the same conversation gets: basics like `PATH`, plus
  that conversation's grants. Before this, a check the model wrote could read
  `OPENROUTER_API_KEY`.

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
