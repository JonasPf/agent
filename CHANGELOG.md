# Changelog

## 2026-09-15

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
