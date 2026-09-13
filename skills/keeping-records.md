---
name: keeping-records
description: How to keep a record that accumulates over time — measurements, appointments, incidents — as files rather than as memory.
---

Some things are not a fact but a series: a weight, a test result, a symptom, a bill, a mood, a
reading from a meter. What matters is the history, and the history only grows. Memory is the wrong
place for it — memory is small, loaded into every prompt, and consolidated by deletion, so a dated
observation there is either dropped or crowding out the standing facts. Keep a series in files.

## Layout

```
INDEX.md            what streams exist, and what each column means
log/<stream>.csv    date,metric,value,unit,note   — one row per observation, appended
notes/<topic>.md    narrative: what was said, what was decided, what to watch
```

A **stream** is a subject that is measured repeatedly — `weight`, `sleep`, `bloodwork`, `energy-bill`.
A **metric** is the thing measured within it, so related numbers taken together share a file:
`blood-pressure.csv` holds `systolic` and `diastolic` rows for the same date.

Anything with a number or a date goes in a CSV. Anything that is a paragraph goes in a markdown note.
A row that needs a sentence to make sense is two things: a short row, and a note.

## Writing

**Append with `bash`. Never with `write`** — `write` replaces the whole file, so one call with stale
content silently erases the history.

```
echo "2026-09-13,weight,81.2,kg,after breakfast" >> log/weight.csv
```

Rules that keep the file readable a year from now:

- **Get the date from `clock`.** Never infer today from the conversation. `YYYY-MM-DD`, or full
  RFC 3339 when the time of day is part of the observation — both sort lexically, which is what makes
  `sort` and `awk` work on the raw file.
- **No commas in a field.** The `note` column is a few words. If it wants a comma, it is a note in
  `notes/`, and the row can say `see notes/2026-09-13-gp.md`.
- **Leave `unit` empty rather than guessing.** `,,` is honest; a wrong unit is a wrong reading.
- **Create a stream by creating its file with a header row**, then add it to `INDEX.md` in the same
  turn. A stream nobody declared is a stream nobody can interpret.
- **Correct with `edit`, not by rewriting.** A typo in one row is one exact replacement. Do not
  quietly delete an observation that turned out to be inconvenient; append the corrected one and say
  in the `note` what it supersedes.

## Reading back

Read the CSV with a command, not by loading the file into context. The file outlives the context
window; a `bash` pipeline does not care how long it has grown.

| Question | Command |
| --- | --- |
| everything in a stream | `cat log/weight.csv` |
| the last ten | `tail -n 10 log/weight.csv` |
| one metric only | `awk -F, '$2=="systolic"' log/blood-pressure.csv` |
| a date range | `awk -F, '$1>="2026-03-01" && $1<="2026-09-01"' log/weight.csv` |
| the trend | `awk -F, 'NR>1{s+=$3;n++} END{print s/n}' log/weight.csv` |
| anything mentioning a word | `grep -ri thyroid log notes` |

Answer from the file, every time. Do not answer a question about the record from what you remember of
the conversation — that is the reading that is most likely to be wrong, and the file is right there.

## What memory is still for

One item, so a conversation can find its way back: that records are kept here, which session holds
them, and which streams matter. Plus the handful of facts that are genuinely always true and would
change an answer — a standing constraint, a diagnosis, a threshold that means "act". Nothing dated.

## The one limit to say out loud

These files live in **this session's working directory**. Another conversation cannot see them; it has
its own. So a record belongs to one long-running conversation, and starting a second one for the same
subject splits the history in two. Say this the first time you create a stream, so the operator is
choosing it rather than discovering it later.
