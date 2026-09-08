'use strict';

const test = require('node:test');
const assert = require('node:assert');
const { toolResult, resultBody, resultPreview, callBody, callPreview,
        eventLabel, eventDetail, eventTime, isFailure,
        speaker, bubbleClass, messageTime, runTime, lateBy, jobCost } = require('./transcript.js');

// The transcript API sends tool_result as a JSON object. Treating it as a
// string silently produced an empty result box: every tool answer in the
// transcript was invisible, which is exactly what the agent must never do.
test('an object result is used as it stands', () => {
  const r = toolResult({ ok: true, content: 'hello' });
  assert.strictEqual(r.ok, true);
  assert.strictEqual(r.content, 'hello');
});

test('a JSON string result is parsed', () => {
  const r = toolResult('{"ok":true,"content":"hello"}');
  assert.strictEqual(r.ok, true);
  assert.strictEqual(r.content, 'hello');
});

test('a plain-text string result is kept as content', () => {
  const r = toolResult('not json at all');
  assert.strictEqual(r.content, 'not json at all');
  assert.notStrictEqual(r.ok, false);
});

test('a missing result is an empty result, not a crash', () => {
  assert.deepStrictEqual(toolResult(null), {});
  assert.deepStrictEqual(toolResult(undefined), {});
});

test('the body of a successful result is its content', () => {
  assert.strictEqual(resultBody({ ok: true, content: '2026-09-01T16:23:08Z\n' }), '2026-09-01T16:23:08Z\n');
});

test('the body of a failed result is its error', () => {
  assert.strictEqual(resultBody({ ok: false, error: 'may not tick that often' }), 'may not tick that often');
});

test('a result with neither content nor error says so rather than rendering blank', () => {
  assert.strictEqual(resultBody({ ok: true }), '(no output)');
  assert.strictEqual(resultBody({}), '(no output)');
});

// The header carries a preview so a collapsed result is distinguishable from
// an empty one without expanding it.
test('the preview is the first line, truncated', () => {
  assert.strictEqual(resultPreview('one\ntwo\nthree'), 'one');
  assert.strictEqual(resultPreview('x'.repeat(90)), 'x'.repeat(72) + '…');
  assert.strictEqual(resultPreview('  padded  '), 'padded');
  assert.strictEqual(resultPreview(''), '');
});


// A call and its result are two halves of the same exchange, so a collapsed
// call previews what it asked for exactly as a collapsed result previews what
// came back.
test('the call preview names the arguments', () => {
  assert.strictEqual(callPreview('{"command":"ls -la","timeout_seconds":30}'),
    'command=ls -la · timeout_seconds=30');
  assert.strictEqual(callPreview('{"path":"notes.txt"}'), 'path=notes.txt');
});

test('the call preview is one line, truncated like a result preview', () => {
  assert.strictEqual(callPreview(JSON.stringify({ text: 'x'.repeat(90) })).length, 73);
  assert.strictEqual(callPreview(JSON.stringify({ text: 'first\nsecond' })), 'text=first second');
});

test('a call with no arguments previews as nothing rather than as braces', () => {
  assert.strictEqual(callPreview('{}'), '');
  assert.strictEqual(callPreview(''), '');
  assert.strictEqual(callPreview(null), '');
});

test('arguments that are not JSON are previewed as they arrived', () => {
  assert.strictEqual(callPreview('not json at all'), 'not json at all');
});

test('an expanded call shows indented arguments, and says when there were none', () => {
  assert.strictEqual(callBody('{"path":"notes.txt"}'), '{\n  "path": "notes.txt"\n}');
  assert.strictEqual(callBody('{}'), '(no arguments)');
  assert.strictEqual(callBody(''), '(no arguments)');
  assert.strictEqual(callBody(null), '(no arguments)');
  assert.strictEqual(callBody('not json at all'), 'not json at all');
});

// An event is a log line, not something anyone said. It has to read as one at a
// glance so it is never mistaken for a message or a tool call.
test('the kind becomes a short upper-case label', () => {
  assert.strictEqual(eventLabel({ event_kind: 'job_check' }), 'JOB CHECK');
  assert.strictEqual(eventLabel({}), 'EVENT');
});

test('the detail carries the status ahead of the text', () => {
  assert.strictEqual(
    eventDetail({ status: 'not_fired', text: 'exit 1' }), 'not fired · exit 1');
  assert.strictEqual(eventDetail({ text: 'continued from an earlier session' }),
    'continued from an earlier session');
  assert.strictEqual(eventDetail({ status: 'fired' }), 'fired');
  assert.strictEqual(eventDetail({}), '');
});

test('the timestamp is a fixed-width wall clock', () => {
  const t = eventTime('2026-09-01T18:38:19.099183+01:00');
  assert.match(t, /^\d{2}:\d{2}:\d{2}$/);
  assert.strictEqual(eventTime(''), '--:--:--');
  assert.strictEqual(eventTime('not a date'), '--:--:--');
});

// Only a genuine failure is coloured. A routine tick that did not fire is the
// normal case and must not read as an error.
test('only failures are marked bad', () => {
  for (const k of ['job_error', 'error']) {
    assert.strictEqual(isFailure({ event_kind: k }), true, k);
  }
  for (const k of ['job_check', 'rotation', 'carried_over']) {
    assert.strictEqual(isFailure({ event_kind: k }), false, k);
  }
  assert.strictEqual(isFailure({ event_kind: 'job_check', status: 'not_fired' }), false);
});

// A job wake is stored with role "user" because that is the only role the model
// API accepts for a turn's input. Labelling it "you" told the operator they had
// typed something they never typed, directly under a chip naming the job.
test('a job wake is labelled as its job, not as the operator', () => {
  assert.strictEqual(speaker({ role: 'user', job_id: '7F3B9C24E0A18D5C42B71' }), 'job (C42B71)');
});

test('a short job id is used whole rather than padded', () => {
  assert.strictEqual(speaker({ role: 'user', job_id: 'J1' }), 'job (J1)');
});

test('a message the operator typed is still theirs', () => {
  assert.strictEqual(speaker({ role: 'user' }), 'you');
});

test('the agent is the agent, wake or not', () => {
  assert.strictEqual(speaker({ role: 'assistant' }), 'agent');
  assert.strictEqual(speaker({ role: 'assistant', job_id: 'J1' }), 'agent');
});

// The grey bubble means "you said this". A wake is not the operator speaking.
test('a wake does not get the operator bubble', () => {
  assert.strictEqual(bubbleClass({ role: 'user' }), 'you');
  assert.strictEqual(bubbleClass({ role: 'user', job_id: 'J1' }), 'job');
  assert.strictEqual(bubbleClass({ role: 'assistant' }), '');
});

// Every message carries when it was said. A time alone is ambiguous once a
// conversation is older than a day, so the date appears exactly when it stops
// being today's.
test('a message from today shows only the clock', () => {
  const now = new Date('2026-09-03T14:05:00');
  assert.strictEqual(messageTime('2026-09-03T09:41:00', now), '09:41');
});

test('a message from another day carries its date', () => {
  const now = new Date('2026-09-03T14:05:00');
  assert.strictEqual(messageTime('2026-09-01T09:41:00', now), '1 Sep 09:41');
});

test('a missing or unparseable timestamp shows nothing rather than NaN', () => {
  const now = new Date('2026-09-03T14:05:00');
  assert.strictEqual(messageTime('', now), '');
  assert.strictEqual(messageTime('not a date', now), '');
  assert.strictEqual(messageTime(undefined, now), '');
});

// The log is read to answer "did this run, and when?", so its stamp always
// carries the date — a bare clock is useless once the job is a day old.
test('a run is stamped with its date and time', () => {
  assert.strictEqual(runTime('2026-09-03T12:34:05'), '3 Sep 12:34');
});

test('a run with no time shows nothing rather than NaN', () => {
  assert.strictEqual(runTime(''), '');
  assert.strictEqual(runTime('not a date'), '');
});

// A wake that ran long after it was due is the visible trace of a host that
// slept, so the log says so where the operator is already looking.
test('a run says how late it was', () => {
  assert.strictEqual(lateBy({ due_at: '2026-09-03T09:00:00', at: '2026-09-03T10:30:00' }), '1h 30m late');
});

test('a punctual run says nothing about lateness', () => {
  assert.strictEqual(lateBy({ due_at: '2026-09-03T09:00:00', at: '2026-09-03T09:00:02' }), '');
  assert.strictEqual(lateBy({ at: '2026-09-03T09:00:02' }), '');
  assert.strictEqual(lateBy({}), '');
});

test('a run late by less than an hour says only the minutes', () => {
  assert.strictEqual(lateBy({ due_at: '2026-09-03T09:00:00', at: '2026-09-03T09:07:00' }), '7m late');
});

// Nothing refuses a cadence now, so the price is what the operator decides on.
// It has to be legible at a glance on the job row.
test('a repeating job states its wakes, tokens, and money', () => {
  const line = jobCost({ wakes_per_day: 144, tokens_per_turn: 5000, tokens_per_day: 720000,
    priced: true, cost_per_day: 2.16, cost_per_month: 64.8 });
  assert.match(line, /144/);
  assert.match(line, /720,000/);
  assert.match(line, /\$2\.16/);
  assert.match(line, /\$64\.80/);
});

test('a gated job says its wakes are free', () => {
  const line = jobCost({ gated: true, tokens_per_turn: 5000 });
  assert.match(line, /no model cost/);
  assert.match(line, /5,000/);
});

test('a one-shot is priced once, not per day', () => {
  const line = jobCost({ wakes_per_day: 0, tokens_per_turn: 5000 });
  assert.match(line, /one wake/);
  assert.doesNotMatch(line, /a day/);
});

// A dollar figure invented from a missing price would be worse than none.
test('an unpriced job shows tokens and no money', () => {
  const line = jobCost({ wakes_per_day: 144, tokens_per_turn: 5000, tokens_per_day: 720000, priced: false });
  assert.match(line, /720,000/);
  assert.doesNotMatch(line, /\$/);
});

test('a job with no estimate says nothing', () => {
  assert.strictEqual(jobCost(null), '');
  assert.strictEqual(jobCost({}), '');
});

// A session identifier is the only way from one conversation to the one it
// continues, and it appears as bare text in seeding and rotation events. Split
// it out so the interface can make it a link rather than 21 characters to copy.
const { splitSessionIds } = require('./transcript.js');

test('a bare session id is split out of surrounding text', () => {
  const parts = splitSessionIds('seeded from 2C41F09B7DA35E86104B7 (size): 13 carried messages');
  assert.deepStrictEqual(parts, [
    { text: 'seeded from ' },
    { text: '2C41F09B7DA35E86104B7', id: '2C41F09B7DA35E86104B7' },
    { text: ' (size): 13 carried messages' },
  ]);
});

test('every id in a line is split out, not only the first', () => {
  const parts = splitSessionIds('2C41F09B7DA35E86104B7 → 8E5D2A70CB1946F3D0A25');
  assert.deepStrictEqual(parts.filter(p => p.id).map(p => p.id),
    ['2C41F09B7DA35E86104B7', '8E5D2A70CB1946F3D0A25']);
});

test('text with no id is one plain part', () => {
  assert.deepStrictEqual(splitSessionIds('nothing to link here'),
    [{ text: 'nothing to link here' }]);
});

test('a hex word that is not an id length is left alone', () => {
  assert.deepStrictEqual(splitSessionIds('DEADBEEF'), [{ text: 'DEADBEEF' }]);
});

test('an id inside a word is not linked', () => {
  const parts = splitSessionIds('x2C41F09B7DA35E86104B7x');
  assert.deepStrictEqual(parts, [{ text: 'x2C41F09B7DA35E86104B7x' }]);
});

test('empty text splits into nothing', () => {
  assert.deepStrictEqual(splitSessionIds(''), []);
});
