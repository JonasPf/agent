---
name: scheduling
description: How to turn a condition into a shell command, and how to choose interval and expiry.
---

Reach for a check first. A job with a `check` costs nothing on a tick where the command exits
non-zero. A job without one calls the model on every tick, in this conversation's full context.

Nearly every condition is a command:

| Asked for | check |
| --- | --- |
| the deploy finishes | `test "$(gh run list -L1 --json status -q '.[0].status')" = completed` |
| the file appears | `test -f /path/to/file` |
| the site is back | `curl -fsS -o /dev/null https://example.com` |
| the PR is merged | `gh pr view 42 --json state -q .state \| grep -qx MERGED` |
| the queue drains | `test "$(redis-cli llen work)" -eq 0` |

Exit zero means the condition is met. Test the command with `bash` before creating the job.

A condition genuinely needs judgement only when no command could decide it: "has this thread turned
hostile", "does this draft still read as defensive". Such a job must state `reason_no_check` and may
not tick more often than every fifteen minutes. It fires by calling `notify`; a run that does not
call `notify` produces no message.

Interval: as often as the condition can change and the check is cheap. Two minutes is reasonable for
a local command.

Expiry: required, and defaults to 24 hours. Choose the point past which the answer stops mattering.
Expiring without firing is not silent — it creates a dead letter and interrupts, so a generous
expiry is safe.

`on_condition_met` is `delete` for a one-shot watch and `continue` for a standing report.
