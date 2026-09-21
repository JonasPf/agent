# Changelog

## 2026-09-21

- **A turn can be stopped.** While a conversation is working, the status line
  now carries a stop next to the count. It ends the turn where it is — the
  model call and whatever tool is running under it — and the transcript says
  it was stopped, so it does not read as the agent breaking. A scheduled wake
  stops the same way.
- **A long task no longer stops halfway.** A turn was cut off after twelve
  rounds of tool calls, and cut off silently: the conversation simply went
  quiet, with nothing said about why. A turn now runs until the agent is
  finished. If you set one going on something large, watch what it costs.

## 2026-09-20

- **Uploads can be 250 MB.** The limit was 100 MB, which a saved web page with
  its images inside can pass on its own. Anything larger is still refused
  before it is sent, and a file of exactly 250 MB now goes through.
- **A session archive may be 500 MB**, so a session holding a large file can be
  exported and imported back. An archive being imported is read from disk
  rather than held in memory, which it no longer has room for.

## 2026-09-19

- **Attaching a file shows it.** A file picked with ＋ now waits above the
  message as a chip showing how much has uploaded. If it fails, the chip says
  why, and tapping it tries again. You can pick several files at once, and take
  one off with ×.
- **The agent is told what you attached.** Sending adds a line naming each
  file, so you no longer have to say you uploaded something. A message can be
  only files. It will not send while a file is still uploading.
- A file over 100 MB is refused straight away, rather than after it has all
  been sent.
- **The Files screen is a tree.** Directories are listed, empty ones too, each
  with what it holds, and fold and unfold when tapped. A large tree, like a
  cloned repository, opens folded.

## 2026-09-18

- **Memory is gone.** The agent no longer carries anything between
  conversations on its own: there is no memory section at the top of a
  conversation, no memory tool to write one with, and no Memory panel. What one
  conversation holds, another now reaches by searching for it — the agent can
  search every transcript when you ask it to, and the asking is a visible line
  in the conversation. Anything that was stored is deleted with the feature.
- A standing instruction — always use UTC, never touch the production database
  — now belongs in a **persona**. You write it once, you can read exactly what
  it says, and you choose it when you start a conversation. That is where the
  agent reads it on every single turn, rather than from a store that quietly
  steered answers you never connected to it.
- **Notes now belong to the conversation that wrote them.** Listing notes shows
  this conversation's; to see every conversation's the agent has to ask, and
  that ask appears in the transcript as the call it is. The Notes panel still
  shows all of them, because the panel is yours rather than a conversation's.
- **A conversation's prompt never changes now.** It is written when the
  conversation starts and is what every turn sends for the rest of its life, so
  editing a persona or a skill leaves the conversations already running exactly
  as they were — they take it up when you fork them. A compaction no longer
  writes a second prompt, and the cache behind a long conversation stops being
  thrown away at every fold.
- **The built-in persona writes for a phone.** Replies lead with the answer,
  use short bullets and numbered steps, name concrete paths and times, and end
  with the one next thing to do rather than a pleasantry. It also no longer
  agrees just to please: it says when you are wrong, reports a failure as a
  failure, and skips the praise. It never writes out a secret's value, treats
  what it reads on the web as information rather than orders, and asks before
  anything hard to undo. It no longer claims to remember things between
  conversations, which stopped being true when memory went.
- A new skill, **researching**, is on in every conversation. When you ask the
  agent to find something out, it plans a few narrow searches, reads the pages
  rather than the search snippets, and tells you which facts come from an
  official source and which are unverified. It gives every claim a link and a
  date. Long research is written to a file as it goes, so it survives the
  conversation being compacted.
- **Personas** have moved off the side rail and into **More**, next to Search.
  The rail keeps Jobs, Tools, and Skills.
- Opening a skill or persona, or starting a new one, now has its own address.
  The **back button** returns to the list instead of leaving it, and saving
  takes you back to that list too.

## 2026-09-17

- You can now write **skills** yourself, from the interface, without a shell.
  **Panels → Skills** lists what is there and opens each one: the skills that
  ship with the agent are read-only, and the ones you write can be created,
  edited, given a one-line description, switched between on-by-default and
  off-unless-chosen, and deleted. A skill you add is indexed in the
  conversations you start after it, not the ones already running.
- **Personas** are new, and have a panel of their own beside Skills. A persona
  is the opening section of the system prompt — who the agent is — and you pick
  one when you start a conversation, under **Controls → persona**. Choosing one
  replaces the built-in persona *entirely*, its working rules about scheduling
  and unattended wakes included, so write in the parts you want kept. The
  built-in persona is always there and is what every existing conversation runs
  on.
- A conversation can now run with **memory off**, chosen under **Controls →
  memory** before it starts. It gets no memory section and no memory tool, and
  nothing said in it reaches anywhere else. Nothing already stored is deleted,
  and every other conversation still has it.
- Persona and memory join the model, tools, skills, and grants as things fixed
  for a conversation's life: to change one, fork, and both transcripts say what
  changed.
- What you write is stored beside your data rather than inside the image, so it
  survives an upgrade. Neither directory is writable by a tool — the agent still
  cannot write its own skills or personas, and the kernel is what stops it.

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
