'use strict';

// Push as a device takes part in it (ADR-066): turning it on from the settings
// screen, registering again when the page opens, the test, and the way out.
// These drive the real functions in app.js against a browser whose service
// worker and push manager are small enough to read.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');

const WEB = __dirname;

function node(tag) {
  return {
    tagName: tag, children: [], style: {}, hidden: false, className: '', id: '', disabled: false,
    _text: '', get textContent() { return this._text; },
    set textContent(v) { this._text = String(v); this.children = []; },
    set innerHTML(v) { this.children = []; }, get innerHTML() { return ''; },
    append(...k) { for (const x of k) if (x) this.children.push(x); },
    appendChild(k) { this.children.push(k); return k; },
    querySelector() { return node('div'); }, remove() {}, addEventListener() {}, setAttribute() {},
    scrollHeight: 0, scrollTop: 0, clientHeight: 0
  };
}

function textOf(n) {
  let out = String(n._text || '');
  for (const c of n.children || []) out += ' ' + textOf(c);
  return out;
}

function button(n, label) {
  for (const c of n.children || []) {
    if (c.tagName === 'button' && c.textContent === label) return c;
    const deeper = button(c, label);
    if (deeper) return deeper;
  }
  return null;
}

const KEY = Buffer.from([4, ...Array.from({ length: 64 }, (_, i) => i + 1)]).toString('base64url');
const OTHER = Buffer.from([4, ...Array(64).fill(9)]).toString('base64url');
const bytes = b64 => new Uint8Array(Buffer.from(b64, 'base64url'));

// subscription is a PushSubscription made under the given key.
function subscription(key) {
  const s = {
    endpoint: 'https://fcm.googleapis.com/fcm/send/abc',
    options: { applicationServerKey: bytes(key).buffer },
    unsubscribed: false,
    toJSON() { return { endpoint: this.endpoint, expirationTime: null, keys: { p256dh: 'BPUB', auth: 'AUTH' } }; },
    async unsubscribe() { s.unsubscribed = true; return true; }
  };
  return s;
}

// load boots app.js in a browser that can be pushed to, or one shaped by
// `browser`. `has` is the subscription the push manager already holds;
// `devices` is what the agent lists.
function load({ permission = 'default', has = null, browser = 'push', devices = [] } = {}) {
  const calls = [];
  const sw = { subscribed: [], listener: null, sub: has };
  const ids = {};
  const ctx = {
    console, setTimeout, clearTimeout, requestAnimationFrame: f => f(), atob,
    document: {
      createElement: node, createTextNode: t => ({ text: t }),
      getElementById: id => ids[id] || (ids[id] = node('div')),
      body: node('body'), addEventListener() {}, hidden: false
    },
    location: { hash: '', protocol: 'https:', host: 'x' },
    navigator: {},
    fetch: async (p, opts) => {
      const method = (opts || {}).method || 'GET';
      calls.push({ method, path: p, body: (opts || {}).body });
      let reply = [];
      if (method === 'GET' && p === '/push') reply = { public_key: KEY, devices };
      if (method === 'POST' && p === '/push/devices') reply = { id: 'D1', service: 'fcm.googleapis.com' };
      if (method === 'POST' && p === '/push/test') reply = { devices: [{ id: 'D1', service: 'fcm.googleapis.com', last_status: 201 }] };
      if (method === 'DELETE') return { ok: true, status: 204, text: async () => '' };
      return { ok: true, status: 200, text: async () => JSON.stringify(reply) };
    },
    WebSocket: function () { return {}; }
  };
  ctx.addEventListener = () => {};
  ctx.window = ctx;
  if (browser !== 'iphone') {
    ctx.Notification = function () {};
    ctx.Notification.permission = permission;
    ctx.Notification.requestPermission = async () => { ctx.Notification.permission = 'granted'; return 'granted'; };
  }
  if (browser === 'iphone') ctx.navigator.standalone = false;
  if (browser === 'push') {
    const reg = {
      pushManager: {
        getSubscription: async () => sw.sub,
        subscribe: async opts => {
          sw.subscribed.push(opts);
          sw.sub = subscription(Buffer.from(opts.applicationServerKey).toString('base64url'));
          return sw.sub;
        }
      }
    };
    ctx.navigator.serviceWorker = {
      register: async () => reg, ready: Promise.resolve(reg),
      addEventListener: (type, f) => { if (type === 'message') sw.listener = f; }
    };
    ctx.PushManager = function () {};
  }
  vm.createContext(ctx);
  for (const f of ['markdown.js', 'transcript.js', 'notify.js', 'models.js', 'app.js']) {
    let src = fs.readFileSync(path.join(WEB, f), 'utf8');
    if (f === 'app.js') src = src.replace(/^boot\(\);$/m, '');
    vm.runInContext(src, ctx, { filename: f });
  }
  // A click redraws the screen; the screen is not what is under test.
  vm.runInContext('render = () => {};', ctx);
  return { ctx, calls, sw };
}

const settle = async () => { for (let i = 0; i < 20; i++) await new Promise(r => setImmediate(r)); };
const posted = (calls, p) => calls.filter(c => c.method === 'POST' && c.path === p);

test('turning push on asks permission, subscribes under the agent key, and registers the device', async () => {
  const { ctx, calls, sw } = load();
  const box = ctx.notifyControl();
  assert.match(textOf(box), /notifications · off/);
  button(box, 'Turn on push notifications').onclick();
  await settle();
  assert.strictEqual(ctx.Notification.permission, 'granted');
  assert.strictEqual(sw.subscribed.length, 1);
  assert.strictEqual(sw.subscribed[0].userVisibleOnly, true);
  assert.deepStrictEqual(Array.from(sw.subscribed[0].applicationServerKey), Array.from(bytes(KEY)));
  const reg = posted(calls, '/push/devices');
  assert.strictEqual(reg.length, 1);
  assert.strictEqual(JSON.parse(reg[0].body).endpoint, 'https://fcm.googleapis.com/fcm/send/abc');
  assert.strictEqual(vm.runInContext('state.push.device', ctx), 'D1');
});

test('a subscribed device says it is pushed to, and its test reports each answer', async () => {
  const { ctx, calls } = load({ permission: 'granted', has: subscription(KEY) });
  vm.runInContext("state.push.device = 'D1';", ctx);
  const box = ctx.notifyControl();
  assert.match(textOf(box), /notifications · push/);
  assert.match(textOf(box), /even with the browser closed/);
  button(box, 'Send a test').onclick();
  await settle();
  assert.strictEqual(posted(calls, '/push/test').length, 1);
  assert.strictEqual(vm.runInContext('state.push.test.length', ctx), 1);
});

// An agent whose state was reset has forgotten the device; the page tells it
// again each time it opens.
test('opening the page registers a subscription it already has, without subscribing again', async () => {
  const has = subscription(KEY);
  const { ctx, calls, sw } = load({ permission: 'granted', has });
  await ctx.startPush();
  assert.strictEqual(posted(calls, '/push/devices').length, 1);
  assert.strictEqual(sw.subscribed.length, 0);
  assert.strictEqual(has.unsubscribed, false);
  assert.strictEqual(vm.runInContext('state.push.device', ctx), 'D1');
});

test('a subscription made under a key the agent no longer holds is replaced', async () => {
  const has = subscription(OTHER);
  const { ctx, calls, sw } = load({ permission: 'granted', has });
  await ctx.startPush();
  assert.strictEqual(has.unsubscribed, true);
  assert.strictEqual(sw.subscribed.length, 1);
  assert.deepStrictEqual(Array.from(sw.subscribed[0].applicationServerKey), Array.from(bytes(KEY)));
  assert.strictEqual(posted(calls, '/push/devices').length, 1);
});

test('a page without permission registers nothing when it opens', async () => {
  const { ctx, calls } = load({ permission: 'default', has: subscription(KEY) });
  await ctx.startPush();
  assert.strictEqual(posted(calls, '/push/devices').length, 0);
});

test('opening a notification sends the open page to its session', async () => {
  const { ctx, sw } = load({ permission: 'granted' });
  await ctx.startPush();
  sw.listener({ data: { open: 'S2' } });
  assert.strictEqual(ctx.location.hash, '#session/S2');
});

test('turning push off unsubscribes and forgets the device', async () => {
  const has = subscription(KEY);
  const { ctx, calls } = load({ permission: 'granted', has });
  vm.runInContext("state.push.device = 'D1';", ctx);
  button(ctx.notifyControl(), 'Turn off on this device').onclick();
  await settle();
  assert.strictEqual(has.unsubscribed, true);
  assert.ok(calls.some(c => c.method === 'DELETE' && c.path === '/push/devices/D1'));
  assert.strictEqual(vm.runInContext('state.push.device', ctx), '');
});

test('a refusal is shown beside its device, which can be forgotten', async () => {
  const { ctx, calls } = load({ permission: 'granted', devices: [
    { id: 'D1', service: 'fcm.googleapis.com', last_status: 201 },
    { id: 'D2', service: 'web.push.apple.com', last_status: 403, last_error: '403 BadJwtToken' }
  ] });
  vm.runInContext("state.push.device = 'D1';", ctx);
  const box = ctx.notifyControl();
  await settle();
  assert.match(textOf(box), /This device · Chrome/);
  assert.match(textOf(box), /web\.push\.apple\.com refused the last push: 403 BadJwtToken/);
  button(box, 'Forget').onclick();
  await settle();
  assert.ok(calls.some(c => c.method === 'DELETE' && c.path === '/push/devices/D2'));
});

test('an iPhone outside the home-screen app is told to add it, and offered no button', () => {
  const { ctx } = load({ browser: 'iphone' });
  const box = ctx.notifyControl();
  assert.match(textOf(box), /Add to Home Screen/);
  assert.strictEqual(button(box, 'Turn on push notifications'), null);
  assert.strictEqual(button(box, 'Enable notifications'), null);
});

test('a browser that cannot be pushed to keeps banners while it is open', () => {
  const { ctx } = load({ browser: 'banners', permission: 'granted' });
  const box = ctx.notifyControl();
  assert.match(textOf(box), /notifications · while open/);
  assert.match(textOf(box), /cannot be pushed to/);
});
