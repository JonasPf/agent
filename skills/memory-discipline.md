---
name: memory-discipline
description: What is worth remembering, and how to consolidate when memory is full.
---

Memory is small, curated, and loaded into every conversation. Transcript search is exhaustive and
large. Memory answers "what is always true"; search answers "what was said that one time".

Write an item when a fact will still be true next month and would change an answer:
preferences, standing constraints, names of the things being worked on, decisions with reasons.

Do not write: anything in the code or the git history, anything specific to the conversation in
progress, anything that will be stale within days.

One fact per item, phrased so it reads correctly with no surrounding context. "prefers Go" is an
item; "we discussed languages" is not.

A write that would exceed capacity fails and returns everything currently stored. Nothing is
dropped automatically. Consolidate in the same turn: merge items that say the same thing, delete
what is no longer true, then retry the write.
