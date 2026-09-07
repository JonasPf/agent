---
name: scheduling
description: How to turn a condition into a shell command, and how to choose a schedule and an ending.
---

A job has three settings that matter. Decide each one; there are no job types.

| Setting | Decide |
| --- | --- |
| `schedule` | When it wakes: a duration from now (`30m`), cron (`0 9 * * 1-5`), or an absolute RFC 3339 time. |
| `check` | Optional. A command run before the prompt; exit zero means act, anything else means skip this wake. |
| `after_acting` | What becomes of the job once it acts: `stop` or `continue`. Default `continue`. |

## Reach for a check first

A wake whose check exits non-zero costs nothing. A wake with no check runs a full turn in this
conversation's context, so a frequent job without one is a real recurring cost. Nothing refuses it:
prefer a check where a command could decide the question, and say what the cadence will cost when it
is tight.

Nearly every condition is a command:

| Asked for | check |
| --- | --- |
| the deploy finishes | `test "$(gh run list -L1 --json status -q '.[0].status')" = completed` |
| the file appears | `test -f /path/to/file` |
| the site is back | `curl -fsS -o /dev/null https://example.com` |
| the PR is merged | `gh pr view 42 --json state -q .state \| grep -qx MERGED` |
| the queue drains | `test "$(redis-cli llen work)" -eq 0` |

Test the command with `bash` before creating the job. A check must answer the moment it runs: never
sleep, poll, or wait inside one. It is killed at sixty seconds and that counts as a failure, not as a
condition that is false.

Leave `check` out only when no command could decide the question — "has this thread turned hostile",
"does this draft still read as defensive".

## Worked examples

| Request | schedule | check | after_acting |
| --- | --- | --- | --- |
| Remind me in five minutes to stretch | `5m` | — | **stop** |
| Wish Ana happy birthday on 14 March | that instant, from `clock` | — | stop |
| Daily briefing at eight | `0 8 * * *` | — | continue |
| Stretch every half hour | `30m` | — | continue |
| Tell me when the deploy finishes | `2m` | the deploy command above | **stop** |
| Watch a site for changes | `1h` | a diff command | continue |
| Watch a thread for hostility | `30m` | — | continue |

## Choosing each one

**schedule** — "in ten minutes", "in two hours" is a duration with `after_acting: stop`. It needs no clock:
`10m` with `after_acting: stop` wakes once, ten minutes from now. Only a named point — "on 14 March", "next
Tuesday at nine" — needs an absolute instant, and the `clock` tool gives you today's date and will do the
arithmetic with its `offset`. An instant already in the past is rejected.

**after_acting** — `stop` when the request is finished once the thing has happened: a one-off reminder,
a watch that ends when its check first passes. `continue` for anything standing. This is not about how
often it wakes; the schedule says that, and a check keeps polling either way.

A job runs until it acts and stops repeating, or until it is deleted. Nothing expires it and nothing
gives up on it: a job that keeps failing keeps its place and piles the failures into its own log,
which is where the operator decides what to do about it. Listing a job shows that log.

You can delete the job you are currently running: pass its id, or omit the id and the running job is
assumed. That is how a standing watch ends itself once it has seen what it was waiting for.
