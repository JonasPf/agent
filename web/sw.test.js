'use strict';

// These run the real sw.js against the events a browser hands a service
// worker: a push arriving, and a notification being opened.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');

function load() {
  const handlers = {};
  const shown = [];
  const opened = [];
  const w = { clients: [] };
  const self = {
    addEventListener: (type, f) => { handlers[type] = f; },
    skipWaiting() {},
    registration: { showNotification: async (title, opts) => { shown.push(Object.assign({ title }, opts)); } },
    clients: {
      matchAll: async () => w.clients,
      openWindow: async url => { opened.push(url); },
      claim: async () => {}
    }
  };
  const ctx = { self, console };
  vm.createContext(ctx);
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'sw.js'), 'utf8'), ctx, { filename: 'sw.js' });
  return { handlers, shown, opened, w };
}

// fire dispatches an event and waits for what it handed waitUntil.
async function fire(handler, event) {
  let pending = Promise.resolve();
  event.waitUntil = p => { pending = p; };
  handler(event);
  await pending;
}

const push = obj => ({ data: { json: () => obj } });

test('a push shows what the agent sent, as sent', async () => {
  const sw = load();
  await fire(sw.handlers.push, push({ title: 'Boiler', body: 'The deploy finished.', tag: 'agent:S2', session_id: 'S2' }));
  assert.strictEqual(sw.shown.length, 1);
  const n = sw.shown[0];
  assert.strictEqual(n.title, 'Boiler');
  assert.strictEqual(n.body, 'The deploy finished.');
  assert.strictEqual(n.tag, 'agent:S2');
  assert.strictEqual(n.data.session_id, 'S2');
});

// Safari withdraws a subscription whose pushes show nothing, so even a push
// that cannot be read shows something.
test('a push that cannot be read still shows a notification', async () => {
  const sw = load();
  await fire(sw.handlers.push, { data: { json: () => { throw new Error('not JSON'); } } });
  await fire(sw.handlers.push, { data: null });
  assert.strictEqual(sw.shown.length, 2);
  assert.strictEqual(sw.shown[0].title, 'agent');
});

const notification = sessionID => ({
  closed: false, data: { session_id: sessionID }, close() { this.closed = true; }
});

test('opening a notification goes to its session in a window already open', async () => {
  const sw = load();
  const said = [];
  let focused = false;
  sw.w.clients = [{ focus: async () => { focused = true; }, postMessage: m => said.push(m) }];
  const n = notification('S2');
  await fire(sw.handlers.notificationclick, { notification: n });
  assert.strictEqual(n.closed, true);
  assert.strictEqual(said.length, 1);
  assert.strictEqual(said[0].open, 'S2');
  assert.strictEqual(focused, true);
  assert.deepStrictEqual(sw.opened, []);
});

test('with no window open, opening a notification opens one at its session', async () => {
  const sw = load();
  await fire(sw.handlers.notificationclick, { notification: notification('S2') });
  assert.deepStrictEqual(sw.opened, ['/#session/S2']);
});

test('a test push, which names no session, opens the app', async () => {
  const sw = load();
  await fire(sw.handlers.notificationclick, { notification: notification('') });
  assert.deepStrictEqual(sw.opened, ['/']);
});
