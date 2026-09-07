---
name: changing-yourself
description: How to propose a change to your own code — clone, edit, test, push a branch, open a pull request, and read what CI says.
---

You can change the agent you are, the interface you are read through, and the tools you call. You
cannot change any of them where they are running. What you can do is propose a change the way a person
does: a branch, a pull request, a green pipeline, and a human who merges it.

Nothing you write reaches production until that person merges. Say so plainly when you finish, and do
not describe a change as done because you pushed it.

## The loop

1. **Clone into this session's working directory.** `git clone $AGENT_REPO repo` — your tools can only
   write here, so this is the only place the checkout can live. If a clone is already there from
   earlier in the conversation, `git -C repo pull` instead of cloning again.
2. **Branch.** `git -C repo switch -c <short-kebab-description>`. Never commit to `main`; a push to
   `main` is refused by the repository.
3. **Read before you write.** `CLAUDE.md` at the root says how this repository is worked in. It is not
   optional: it is the difference between a change that merges and one that is closed.
4. **Change one thing.** A pull request that does two unrelated things is two pull requests.
5. **Run the checks.** `cd repo && task check`. If the toolchain is missing here, say so and let the
   pipeline be the check — but then expect to iterate, and read the failure before pushing again.
6. **Commit and push.** A subject line saying what changed and why, then
   `git -C repo push -u origin <branch>`.
7. **Open the pull request.** `gh pr create --fill` — or with a body you wrote, which is better. State
   what changed, what you tested, and what you were unsure about.
8. **Read the pipeline.** `gh pr checks --watch`. A red check is yours to fix: push again on the same
   branch. Do not ask for a merge over a failing pipeline.
9. **Hand it over.** Say the pull request number and what you want looked at. Then stop.

## What must be true of the change

The repository's own rules apply to you exactly as they apply to anyone:

- **Test first.** Write the test that fails, then the code that passes it. A change with no test
  covering it is not finished.
- **Never weaken a test to get green.** No skips, no loosened assertions, no commented-out checks. A
  failing test is information.
- **Keep the specification in step.** `specs/` and the code are two views of one system. If behaviour
  changes, the spec changes in the same pull request.
- **A tool ships eval cases.** A new tool directory without `eval.json` fails the test run.

## What a change to yourself costs

Adding or changing a tool is cheap and needs no restart once it is merged and deployed: the image
carries the new binary and `reload_tools` picks it up. Changing the agent itself replaces the running
process — the conversation, its jobs, and its files survive, because they are on disk, but anything
mid-flight does not. Say when a change you are proposing is that kind.

## When not to do this

- The operator asked a question. Answer it. Do not open a pull request to answer a question.
- The change is to something you cannot test. Say what you would change and why, and let them decide.
- You have already opened a pull request for this and it has not been reviewed. Add to that branch
  rather than opening a second one.
