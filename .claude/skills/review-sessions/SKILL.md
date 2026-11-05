---
name: review-sessions
description: Use when Jonas asks to review the agent's conversations (all of them, recent ones, or one by id) to find where the deployed agent went wrong, worked around something, or wasted rounds, trace each to its cause in this repository, and suggest improvements. The sessions live on his server, not in this repository.
---

# Review the agent's sessions

The sessions show what the spec, the skills, and the tools actually produce. A review reads them,
finds where the agent went wrong or worked around something, and traces each finding to its cause
here: a skill's wording, a tool's description, a script, the gateway, or the spec itself. It
suggests. It changes nothing until Jonas picks what to fix.

## Where the sessions are

- On Jonas's server, not in this repository. The agent runs there in Docker, deployed by Dokploy.
  The ssh target is `REVIEW_SSH` in `.env`, which is not committed; if it is missing, ask Jonas for
  it. In the state volume, sessions are in `data/sessions/<id>/` (`meta.json`, `transcript.jsonl`),
  each session's files in `workspace/<id>/`.
- `data/sessions/` in a local checkout holds stale development leftovers. Never review those.
- Read sessions only through `scripts/sessions.py`, piped over ssh. It runs on the server and prints
  what a review needs, so no transcript is copied anywhere:
  ```
  ssh "$(sed -n 's/^REVIEW_SSH=//p' .env)" python3 - list < .claude/skills/review-sessions/scripts/sessions.py
  ssh "$(sed -n 's/^REVIEW_SSH=//p' .env)" python3 - failures ID < .claude/skills/review-sessions/scripts/sessions.py
  ssh "$(sed -n 's/^REVIEW_SSH=//p' .env)" python3 - show ID [--from SEQ --to SEQ] < .claude/skills/review-sessions/scripts/sessions.py
  ```
- If the permission check refuses an ssh call, do not work around it. Give Jonas the exact command
  with `!` in front, so he can run it in this session, and carry on from its output.
- Never write to the server.

## Steps

1. **List** every session with `list`: one line each, oldest first, with user messages, rounds,
   tool calls, calls that failed, calls repeated right after a failure, and cost. A session it
   cannot read is itself a finding.
2. **Read every session** (or the ones Jonas named), oldest first. A short one: `show ID`. A long
   one, hundreds of rounds: `failures ID` first, then `show` with `--from`/`--to` around each failure
   and around Jonas's messages. Look for:
   - a tool call that failed, and what the agent did next
   - a workaround: the agent steering a script somewhere it was not meant to go (in
     `1A19D95E37634318FDB39` it added a 0.1 s cut to force a re-encode)
   - Jonas correcting the agent, repeating a request, or saying it got something wrong
   - a skill step skipped, done out of order, or done by hand when a script does it
   - an argument that changed nothing: Whisper's `prompt` was dropped by the gateway until
     2026-11-03, and only the code showed it
   - rounds or cost out of proportion to the task
   - a report claiming more than was checked
   - an `EVENT` line: a compaction (`covers_through`), a revert, an error
3. **Note each finding as you go**, in a file in your scratchpad directory (not in this repository),
   with the session id and seq numbers. Twenty sessions do not fit in one context, and a compaction
   must not lose them.
4. **Trace each finding to its cause**, and check it still applies:
   - What the agent was told: the skill it used (`skills/<name>/SKILL.md`), the tool's description
     (`tools/<name>/manifest.json`), and the spec (`specs/`). Where they disagree, the spec wins.
   - What ran: a skill's script (`skills/*/scripts`), the tool (`tools/<name>`), the gateway and the
     loop (`internal/app`).
   - Whether it was fixed after the session: `git log --since=<the session's date> -- <paths>` and
     `CHANGELOG.md`. Drop a finding that is already fixed.
   - Reproduce what can be reproduced locally before naming a cause: ffmpeg on a made-up clip, a
     script on a fixture. A cause read off a transcript can be wrong. A review on 2026-11-03 first
     blamed a flaky test on layout timing, and the cause was a message race in the panel.
5. **Report** in the terminal, most costly first, at most ten. For each: what happened (session id,
   seq), the cause (`file:line`), the fix, and roughly how long it takes. Then ask which to make.
   Each one picked is an ordinary change under `AGENTS.md`: its own branch from `main`, a test
   first, specs and changelog, `task check`, a pull request.

## Privacy

- Sessions hold personal details, some of them sensitive. Read them like any other, but never copy a value from them into the report, the notes,
  a commit, a test, an issue, or a pull request. Cite the session id and seq, and describe the
  pattern.

## Do not

- Fix anything during the review.
- Read local `data/sessions/`, or copy transcripts off the server.
- Report a finding without its evidence: session id and seq, and the line of code or spec it
  traces to.
