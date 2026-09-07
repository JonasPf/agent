'use strict';

// These drive the real handle() in app.js, so they cover the path an entry
// actually takes: WebSocket event → decision → the browser's Notification
// constructor. The helper tests in notify.test.js check the decision alone;
// this checks that the page is wired to it.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');

const WEB = __dirname;

function node(tag) {
  return {
    tagName: tag, children: [], style: {}, hidden: false, className: '', id: '',
    _text: '', get textContent() { return this._text; },
    set textContent(v) { this._text = String(v); },
    set innerHTML(v) { this.children = []; }, get innerHTML() { return ''; },
    append(...k) { for (const x of k) if (x) this.children.push(x); },
    appendChild(k) { this.children.push(k); return k; },
    querySelector() { return node('div'); }, remove() {}, addEventListener() {},
    scrollHeight: 0, scrollTop: 0, clientHeight: 0
  };
}

// load builds a page with the given notification permission and visibility, and
// returns the context plus every notification the page constructed.
function load({ permission = 'granted', hidden = false } = {}) {
  const shown = [];
  const ids = {};
  const ctx = {
    console, setTimeout, clearTimeout, requestAnimationFrame: f => f(),
    document: {
      createElement: node, createTextNode: t => ({ text: t }),
      getElementById: id => ids[id] || (ids[id] = node('div')),
      body: node('body'), addEventListener() {}, hidden
    },
    location: { hash: '#session/S1', protocol: 'http:', host: 'x' },
    navigator: {},
    // boot() renders the session list; an empty list is enough for it to finish.
    fetch: async () => ({ ok: true, status: 200, text: async () => '[]' }),
    WebSocket: function () { return {}; }
  };
  ctx.Notification = function (title, opts) {
    shown.push({ title, body: opts && opts.body, tag: opts && opts.tag });
  };
  ctx.Notification.permission = permission;
  ctx.addEventListener = () => {};
  ctx.window = ctx;
  vm.createContext(ctx);
  for (const f of ['markdown.js', 'transcript.js', 'notify.js', 'app.js']) {
    let src = fs.readFileSync(path.join(WEB, f), 'utf8');
    // boot() opens a WebSocket and renders a screen; neither is under test here,
    // and its network calls would outlive the test as unhandled rejections.
    if (f === 'app.js') src = src.replace(/^boot\(\);$/m, '');
    vm.runInContext(src, ctx, { filename: f });
  }
  return { ctx, shown };
}

const entryEvent = (sessionID, over = {}) => ({
  kind: 'entry', session_id: sessionID,
  entry: Object.assign({ type: 'message', role: 'assistant', text: 'The deploy finished.' }, over)
});

function inSession(ctx, id) {
  vm.runInContext(`state.view = 'session'; state.arg = ${JSON.stringify(id)}; state.entries = [];`, ctx);
}

test('a message in another session raises a notification', () => {
  const { ctx, shown } = load();
  inSession(ctx, 'S1');
  ctx.handle(entryEvent('S2'));
  assert.strictEqual(shown.length, 1);
  assert.match(shown[0].body, /deploy finished/i);
});

test('a message in the conversation on screen raises none', () => {
  const { ctx, shown } = load();
  inSession(ctx, 'S1');
  ctx.handle(entryEvent('S1'));
  assert.strictEqual(shown.length, 0);
});

test('a hidden page is notified about its own open session', () => {
  const { ctx, shown } = load({ hidden: true });
  inSession(ctx, 'S1');
  ctx.handle(entryEvent('S1'));
  assert.strictEqual(shown.length, 1);
});

test('without permission nothing is shown', () => {
  const { ctx, shown } = load({ permission: 'default' });
  inSession(ctx, 'S1');
  ctx.handle(entryEvent('S2'));
  assert.strictEqual(shown.length, 0);
});

test('a tool-only turn and a user message raise nothing', () => {
  const { ctx, shown } = load();
  inSession(ctx, 'S1');
  ctx.handle(entryEvent('S2', { text: '', tool_calls: [{ name: 'bash' }] }));
  ctx.handle(entryEvent('S2', { role: 'user', text: 'hello' }));
  assert.strictEqual(shown.length, 0);
});

test('successive messages in one session share a tag', () => {
  const { ctx, shown } = load();
  inSession(ctx, 'S1');
  ctx.handle(entryEvent('S2'));
  ctx.handle(entryEvent('S2', { text: 'And the tests passed.' }));
  assert.strictEqual(shown.length, 2);
  assert.strictEqual(shown[0].tag, shown[1].tag);
});

// The transcript still has to render while all this happens.
test('the open session still renders its new entry', () => {
  const { ctx } = load();
  inSession(ctx, 'S1');
  ctx.handle(entryEvent('S1'));
  const n = vm.runInContext('state.entries.length', ctx);
  assert.strictEqual(n, 1);
});
