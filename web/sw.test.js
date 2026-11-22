'use strict';

// These run the real sw.js against the events a browser hands a service
// worker: a push arriving, and a notification being opened.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');

function load(online) {
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
  self.navigator = { onLine: online !== false };
  const ctx = { self, console, Response };
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

// ---------- a page opened offline ----------

// open hands the worker a request as a browser does, and says what it
// answered with, or null when it left the request to the browser.
function open(sw, mode) {
  let answered = null;
  sw.handlers.fetch({ request: { mode, url: 'https://agent.example/' }, respondWith: r => { answered = r; } });
  return answered;
}

// Opened with no connection at all, the app is a page of its own saying so
// rather than the browser's error (webui.html, "Installation").
test('a page opened while the device is offline is answered by a page saying so', async () => {
  const sw = load(false);
  const r = await open(sw, 'navigate');
  assert.ok(r, 'the worker left an offline page to the browser\'s error');
  assert.match(r.headers.get('content-type'), /text\/html/);
  const page = await r.text();
  assert.match(page, /This device is offline/);
  assert.match(page, /Try again/);
  assert.match(page, /addEventListener\('online'/, 'the page does not open the app once the connection is back');
});

// With a connection the browser asks the agent itself, as it would with no
// worker: the password in front of the agent is asked for by the browser, and
// a worker passing a refusal on can leave it unasked. What a page asks for,
// once opened, is left alone either way; its own screens say what failed.
test('a page opened with a connection, and every other request, is left to the browser', () => {
  assert.strictEqual(open(load(true), 'navigate'), null, 'a page opened online went through the worker');
  assert.strictEqual(open(load(false), 'cors'), null, 'a request a page made went through the worker');
  assert.strictEqual(open(load(true), 'cors'), null);
});
