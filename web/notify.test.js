'use strict';

const test = require('node:test');
const assert = require('node:assert');
const { shouldNotify, messageNotification, canShowSystemNotification,
  pushSupport, keyBytes, sameKey, deviceLine } = require('./notify.js');

const msg = (over = {}) => Object.assign(
  { type: 'message', role: 'assistant', text: 'The deploy finished.' }, over);

// The point is to learn about output you are not looking at. A message arriving
// in the conversation already on screen needs no announcement — you can see it.
test('a message in the session on screen does not notify', () => {
  assert.strictEqual(shouldNotify(msg(), 'S1',
    { view: 'session', arg: 'S1', visible: true }), false);
});

test('a message in another session notifies', () => {
  assert.strictEqual(shouldNotify(msg(), 'S2',
    { view: 'session', arg: 'S1', visible: true }), true);
});

test('a message notifies when the page is hidden, even in the open session', () => {
  assert.strictEqual(shouldNotify(msg(), 'S1',
    { view: 'session', arg: 'S1', visible: false }), true);
});

test('a message notifies while the operator is on any other screen', () => {
  assert.strictEqual(shouldNotify(msg(), 'S1',
    { view: 'sessions', arg: undefined, visible: true }), true);
});

// Only what the agent said. The operator's own message and a job's wake are both
// user entries, and announcing those tells them what they already know.
test('a user entry never notifies', () => {
  assert.strictEqual(shouldNotify(msg({ role: 'user', text: 'hello' }), 'S2',
    { view: 'sessions', visible: true }), false);
});

test('a tool result never notifies', () => {
  assert.strictEqual(shouldNotify(msg({ role: 'tool', text: 'ok' }), 'S2',
    { view: 'sessions', visible: true }), false);
});

test('an event entry never notifies', () => {
  assert.strictEqual(shouldNotify(msg({ type: 'event', role: '', text: 'job ran' }), 'S2',
    { view: 'sessions', visible: true }), false);
});

// A turn that only called tools has said nothing yet. Announcing it would buzz
// once per tool round for a single answer.
test('an assistant entry with no text never notifies', () => {
  assert.strictEqual(shouldNotify(msg({ text: '', tool_calls: [{ name: 'bash' }] }), 'S2',
    { view: 'sessions', visible: true }), false);
});

test('a missing entry is not a crash', () => {
  assert.strictEqual(shouldNotify(null, 'S2', { view: 'sessions', visible: true }), false);
});

// One notification per conversation, replaced as it goes: a long exchange in a
// session you are not reading should leave one line, not twenty.
test('the tag is the session, so a conversation replaces itself', () => {
  const a = messageNotification(msg(), 'S1', 'Deploy watch');
  const b = messageNotification(msg({ text: 'And the tests passed.' }), 'S1', 'Deploy watch');
  assert.strictEqual(a.tag, b.tag);
  assert.notStrictEqual(messageNotification(msg(), 'S2', 'Other').tag, a.tag);
});

test('the notification carries the session title and the message', () => {
  const n = messageNotification(msg(), 'S1', 'Deploy watch');
  assert.strictEqual(n.title, 'Deploy watch');
  assert.match(n.body, /deploy finished/i);
  assert.strictEqual(n.sessionID, 'S1');
});

test('a session with no known title still gets one', () => {
  assert.strictEqual(messageNotification(msg(), 'S1', '').title, 'agent');
});

// A whole answer will not fit on a banner, and a banner is not where it should
// be read. It is a pointer to the conversation, so it is cut short on purpose.
test('a long message is clipped to a readable line', () => {
  const n = messageNotification(msg({ text: 'x'.repeat(400) }), 'S1', 'T');
  assert.ok(n.body.length < 200, 'body was ' + n.body.length + ' characters');
});

test('a multi-line message is announced by its first line', () => {
  const n = messageNotification(msg({ text: 'Done.\nHere is the detail.' }), 'S1', 'T');
  assert.strictEqual(n.body, 'Done.');
});

test('a granted permission means the operating system shows it', () => {
  assert.strictEqual(canShowSystemNotification({ hasNotification: true, permission: 'granted' }), true);
});

test('anything else falls back to the unread count alone', () => {
  assert.strictEqual(canShowSystemNotification({ hasNotification: true, permission: 'default' }), false);
  assert.strictEqual(canShowSystemNotification({ hasNotification: true, permission: 'denied' }), false);
  assert.strictEqual(canShowSystemNotification({ hasNotification: false, permission: 'granted' }), false);
  assert.strictEqual(canShowSystemNotification(null), false);
});

test('a browser with a service worker, push, and notifications can be pushed to', () => {
  assert.strictEqual(pushSupport({ hasServiceWorker: true, hasPushManager: true, hasNotification: true }), 'push');
  assert.strictEqual(pushSupport({ hasServiceWorker: true, hasPushManager: true, hasNotification: true, standalone: true }), 'push');
});

// An iPhone outside the home-screen app has none of the API, and is the one
// browser that says it is not standalone.
test('an iPhone outside the home screen is told to use it', () => {
  assert.strictEqual(pushSupport({ hasServiceWorker: true, standalone: false }), 'home-screen');
});

test('anything else cannot be pushed to', () => {
  assert.strictEqual(pushSupport({ hasServiceWorker: true, hasNotification: true }), 'none');
  assert.strictEqual(pushSupport(null), 'none');
});

test('the served key is decoded to the bytes a subscription is made with', () => {
  const raw = Buffer.from([4, ...Array.from({ length: 64 }, (_, i) => i * 3 % 256)]);
  assert.deepStrictEqual(Array.from(keyBytes(raw.toString('base64url'))), Array.from(raw));
});

test('a subscription is recognised as made under the current key, and not under another', () => {
  const key = Buffer.from([4, ...Array(64).fill(7)]).toString('base64url');
  assert.strictEqual(sameKey(keyBytes(key).buffer, key), true);
  assert.strictEqual(sameKey(new Uint8Array(65).buffer, key), false);
  assert.strictEqual(sameKey(null, key), false);
});

// A push that fails looks exactly like a quiet agent, so the refusal is what
// the line says, in full.
test('a device line says what its push service last answered', () => {
  const refused = deviceLine({ service: 'web.push.apple.com', last_status: 403, last_error: '403 BadJwtToken' }, false);
  assert.strictEqual(refused.bad, true);
  assert.match(refused.said, /refused.*403 BadJwtToken/);
  assert.strictEqual(refused.who, 'Safari');
  const fine = deviceLine({ service: 'fcm.googleapis.com', last_status: 201 }, true);
  assert.strictEqual(fine.bad, false);
  assert.match(fine.said, /accepted/);
  assert.strictEqual(fine.who, 'This device · Chrome');
  assert.match(deviceLine({ service: 'push.example' }, false).said, /Nothing pushed yet/);
});
