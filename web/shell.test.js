'use strict';

// The shell is the part of the interface that is the same on every screen: a
// rail of conversations, a header that carries the current view's actions, and
// one pane between them, plus the panels that fill that pane. These drive the
// real functions in app.js, and the stylesheet rules that decide which shape the
// rail takes, because at what width the rail stops being a drawer is a
// requirement and not a detail.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');

const WEB = __dirname;

function node(tag) {
  const n = {
    tagName: tag, children: [], style: {}, hidden: false, className: '', id: '',
    title: '', type: '', value: '', parent: null, _text: '', _html: null,
    get textContent() { return this._text; },
    set textContent(v) { this._text = String(v); this._html = null; this.children = []; },
    set innerHTML(v) { this._html = String(v); this.children = []; },
    get innerHTML() { return this._html == null ? this._text : this._html; },
    append(...k) {
      for (const x of k) {
        if (!x) continue;
        if (x.tagName === '#fragment') { for (const c of x.children) { c.parent = this; this.children.push(c); } }
        else { x.parent = this; this.children.push(x); }
      }
    },
    appendChild(k) { k.parent = this; this.children.push(k); return k; },
    querySelector(sel) { return find(this, sel.replace(/^\./, '')); },
    setAttribute() {}, focus() {}, addEventListener() {},
    remove() {
      if (!this.parent) return;
      this.parent.children = this.parent.children.filter(c => c !== this);
      this.parent = null;
    },
    scrollHeight: 0, scrollTop: 0, clientHeight: 0
  };
  return n;
}

function find(n, cls) {
  for (const c of n.children || []) {
    if (String(c.className || '').split(/\s+/).includes(cls)) return c;
    const deeper = find(c, cls);
    if (deeper) return deeper;
  }
  return null;
}

function findAll(n, cls, out) {
  out = out || [];
  for (const c of n.children || []) {
    if (String(c.className || '').split(/\s+/).includes(cls)) out.push(c);
    findAll(c, cls, out);
  }
  return out;
}

const session = over => Object.assign({
  id: 'S1', title: 'Greenhouse sensors', model: 'x/y', status: 'active', entry_count: 3,
  context_used: 10, rotate_at_tokens: 40000, cost: 0, disk_bytes: 0,
  last_active_at: new Date().toISOString(), unread: 0
}, over);

// load boots app.js against a DOM small enough to read, with the pages the rail
// asks for served from `routes`.
function load(routes) {
  const ids = {};
  const ctx = {
    console, setTimeout, clearTimeout, requestAnimationFrame: f => f(),
    document: {
      createElement: node, createTextNode: t => ({ text: t, textContent: t }),
      createDocumentFragment: () => node('#fragment'),
      getElementById: id => ids[id] || (ids[id] = node('div')),
      body: node('body'), addEventListener() {}, hidden: false
    },
    location: { hash: '', protocol: 'http:', host: 'x' },
    history: { length: 1 },
    navigator: {},
    fetch: async (p, opts) => {
      const method = (opts || {}).method || 'GET';
      (ctx._calls = ctx._calls || []).push({ path: p, method });
      const body = (routes || {})[p];
      if (method !== 'GET') return { ok: true, status: 204, text: async () => '' };
      return { ok: true, status: 200, text: async () => JSON.stringify(body == null ? [] : body) };
    },
    confirm: () => ctx._confirm !== false,
    WebSocket: function () { return {}; }
  };
  ctx.Notification = function () {};
  ctx.Notification.permission = 'default';
  ctx.addEventListener = () => {};
  ctx.matchMedia = q => ({ matches: /pointer: fine/.test(q) });
  ctx.window = ctx;
  vm.createContext(ctx);
  for (const f of ['markdown.js', 'transcript.js', 'notify.js', 'app.js']) {
    let src = fs.readFileSync(path.join(WEB, f), 'utf8');
    if (f === 'app.js') src = src.replace(/^boot\(\);$/m, '');
    vm.runInContext(src, ctx, { filename: f });
  }
  ctx._id = id => ids[id] || (ids[id] = node('div'));
  return ctx;
}

// ---------- the rail ----------

test('the rail lists every conversation, active ones first', async () => {
  const ctx = load({
    '/sessions': [
      session({ id: 'A', title: 'Old', status: 'archived', last_active_at: '2026-09-01T00:00:00Z' }),
      session({ id: 'B', title: 'Newer', last_active_at: '2026-09-05T00:00:00Z' }),
      session({ id: 'C', title: 'Newest', last_active_at: '2026-09-06T00:00:00Z' }),
    ]
  });
  await ctx.renderSidebar();
  const rows = findAll(ctx._id('side-list'), 'side-item');
  assert.deepStrictEqual(rows.map(r => find(r, 'n').textContent), ['Newest', 'Newer', 'Old']);
  assert.ok(String(rows[2].className).split(/\s+/).includes('arch'), 'an archived conversation is not marked as one');
});

// The rail is on screen while a conversation is, so it has to say which of its
// rows is the one being read.
test('the rail marks the conversation on screen', async () => {
  const ctx = load({ '/sessions': [session({ id: 'A', title: 'One' }), session({ id: 'B', title: 'Two' })] });
  vm.runInContext("state.view = 'session'; state.arg = 'B';", ctx);
  await ctx.renderSidebar();
  const rows = findAll(ctx._id('side-list'), 'side-item');
  const on = rows.filter(r => String(r.className).split(/\s+/).includes('on'));
  assert.strictEqual(on.length, 1, 'exactly one row should be marked current');
  assert.strictEqual(find(on[0], 'n').textContent, 'Two');
});

test('a rail row carries the unread count and opens its conversation', async () => {
  const ctx = load({});
  const row = ctx.sideItem(session({ id: 'S9', title: 'Kiln firing log', unread: 3 }));
  assert.strictEqual(find(row, 'badge').textContent, '3');
  row.onclick();
  assert.strictEqual(ctx.location.hash, '#session/S9');
});

test('the rail says so when there is nothing to list', async () => {
  const ctx = load({ '/sessions': [] });
  await ctx.renderSidebar();
  assert.match(find(ctx._id('side-list'), 's').textContent, /No conversations/);
});

// What the gateway has left is a fact about the whole agent, so it belongs to
// the rail rather than to a screen that has to be opened to see it.
test('the rail states the credit remaining and whether tools are sandboxed', () => {
  const ctx = load({});
  vm.runInContext("state.status = { key: { usage: 1.5, remaining: 8.25 }, sandbox: { mechanism: 'seatbelt' } };", ctx);
  ctx.renderSideFoot();
  const text = findAll(ctx._id('side-foot'), 'l').map(l => l.children.map(c => c.textContent || c.text || '').join('')).join(' | ');
  assert.match(text, /8\.2500 left/);
  assert.match(text, /seatbelt/);
});

test('a sandbox that is not enforced says so rather than naming a mechanism', () => {
  const ctx = load({});
  vm.runInContext("state.status = { sandbox: { mechanism: 'none', reason: 'no sandbox-exec' } };", ctx);
  ctx.renderSideFoot();
  const text = findAll(ctx._id('side-foot'), 'l').map(l => l.children.map(c => c.textContent || c.text || '').join('')).join(' ');
  assert.match(text, /not enforced/);
});

// ---------- the drawer ----------

test('the rail opens as a drawer and closes on the scrim', () => {
  const ctx = load({});
  ctx._id('menu').onclick();
  assert.strictEqual(ctx.document.body.className, 'drawer');
  ctx._id('scrim').onclick();
  assert.strictEqual(ctx.document.body.className, '');
});

// Following a link with the drawer open would leave it covering the screen the
// link went to.
test('going somewhere closes the drawer', () => {
  const ctx = load({});
  ctx._id('menu').onclick();
  ctx.location.hash = '#jobs';
  ctx.route();
  assert.strictEqual(ctx.document.body.className, '');
});

// ---------- the header ----------

test("a view's actions are in the header and behind the fold, from one list", () => {
  const ctx = load({});
  let opened = '';
  ctx.setHeader('Greenhouse sensors', true, [
    { label: 'Jobs', fn: () => opened = 'jobs' },
    { label: 'Files', fn: () => opened = 'files' },
  ]);
  const inline = ctx._id('head-acts').children.map(b => b.textContent);
  assert.deepStrictEqual(inline, ['Jobs', 'Files']);

  ctx._id('more').onclick({ stopPropagation() {} });
  const menu = find(ctx._id('more-wrap'), 'menu');
  assert.ok(menu, 'the folded actions opened no menu');
  assert.deepStrictEqual(menu.children.map(b => b.textContent), inline, 'the two shapes disagree');
  menu.children[0].onclick();
  assert.strictEqual(opened, 'jobs');
  assert.strictEqual(find(ctx._id('more-wrap'), 'menu'), null, 'the menu stayed open after acting');
});

test('a view with no actions offers no fold', () => {
  const ctx = load({});
  ctx.setHeader('Tools', true);
  assert.strictEqual(ctx._id('more-wrap').hidden, true);
  assert.strictEqual(ctx._id('head-acts').children.length, 0);
});

test('the header says whether there is a way back', () => {
  const ctx = load({});
  ctx.setHeader('agent', false);
  assert.strictEqual(ctx._id('back').hidden, true);
  ctx.setHeader('Jobs', true);
  assert.strictEqual(ctx._id('back').hidden, false);
});

// ---------- the jobs panel ----------

const job = over => Object.assign({
  id: 'J1', session_id: 'S1', prompt: 'Remind me to stretch', schedule: '10m',
  check: '', after_acting: 'continue', status: 'scheduled', run_count: 1,
  next_run_at: new Date(Date.now() + 60000).toISOString(), last_status: ''
}, over);

// clearButton returns the button that deletes finished jobs, if the panel drew
// one at all.
const clearButton = v => findAll(v, 'btn').find(b => /finished/i.test(b.textContent || ''));

// A finished job is kept for its log, so a session that reminds every ten
// minutes buries its live jobs under its dead ones. Clearing them is one action.
test('the jobs panel clears every finished job in one action', async () => {
  const ctx = load({ '/jobs': [job({ id: 'A', status: 'done' }), job({ id: 'B', status: 'done' }), job({ id: 'C' })] });
  const v = ctx._id('view');
  await ctx.viewJobs(v);
  const clear = clearButton(v);
  assert.ok(clear, 'the panel offers no way to clear finished jobs');
  assert.match(clear.textContent, /2 finished jobs/);
  await clear.onclick();
  assert.ok(ctx._calls.some(c => c.method === 'DELETE' && c.path === '/jobs?status=done'),
    'the button deleted nothing: ' + JSON.stringify(ctx._calls));
});

test('nothing finished, nothing to clear', async () => {
  const ctx = load({ '/jobs': [job({ id: 'A' }), job({ id: 'B' })] });
  const v = ctx._id('view');
  await ctx.viewJobs(v);
  assert.strictEqual(clearButton(v), undefined, 'a clear was offered with nothing to clear');
});

// The delete is the one action here that cannot be undone, and the log goes with
// the job, so it asks first.
test('a declined confirmation clears nothing', async () => {
  const ctx = load({ '/jobs': [job({ id: 'A', status: 'done' })] });
  ctx._confirm = false;
  const v = ctx._id('view');
  await ctx.viewJobs(v);
  await clearButton(v).onclick();
  assert.ok(!ctx._calls.some(c => c.method === 'DELETE'),
    'a declined confirmation still deleted: ' + JSON.stringify(ctx._calls));
});

// The panel can be opened for one conversation, and what it clears is what it
// is showing.
test('a clear from one conversation names that conversation', async () => {
  const ctx = load({ '/jobs?session_id=S9': [job({ id: 'A', status: 'done', session_id: 'S9' })] });
  vm.runInContext("state.view = 'jobs'; state.arg = 'S9';", ctx);
  const v = ctx._id('view');
  await ctx.viewJobs(v);
  await clearButton(v).onclick();
  assert.ok(ctx._calls.some(c => c.method === 'DELETE' && c.path === '/jobs?status=done&session_id=S9'),
    'the clear was not scoped to the conversation: ' + JSON.stringify(ctx._calls));
});

// ---------- the composer ----------

// A keyboard expects Enter to send. A phone has no comfortable shift, so there
// Enter has to stay a newline or every second message is sent half-written.
test('a keyboard sends on Enter and breaks the line on shift', () => {
  const ctx = load({});
  const key = over => Object.assign({ key: 'Enter' }, over);
  assert.strictEqual(ctx.sendsOnEnter(key(), true), true);
  assert.strictEqual(ctx.sendsOnEnter(key({ shiftKey: true }), true), false);
  assert.strictEqual(ctx.sendsOnEnter(key({ altKey: true }), true), false);
});

test('a touch screen sends only on the platform combination', () => {
  const ctx = load({});
  const key = over => Object.assign({ key: 'Enter' }, over);
  assert.strictEqual(ctx.sendsOnEnter(key(), false), false);
  assert.strictEqual(ctx.sendsOnEnter(key({ metaKey: true }), false), true);
  assert.strictEqual(ctx.sendsOnEnter(key({ ctrlKey: true }), false), true);
});

// A character being composed — an accent, a kana, a pinyin candidate — ends on
// Enter, and sending there would cut the word in half.
test('a key that finishes a composition sends nothing', () => {
  const ctx = load({});
  assert.strictEqual(ctx.sendsOnEnter({ key: 'Enter', isComposing: true }, true), false);
  assert.strictEqual(ctx.sendsOnEnter({ key: 'a' }, true), false);
});

// ---------- the pane ----------

// A transcript is read in a column; a list of jobs or tools is read across the
// width the screen actually has.
test('a list view takes the room a transcript does not', () => {
  const ctx = load({ '/sessions/S1': { session: session() }, '/sessions/S1/transcript': [] });
  ctx.location.hash = '#jobs';
  ctx.route();
  assert.match(ctx._id('view').className, /roomy/);
  ctx.location.hash = '#session/S1';
  ctx.route();
  assert.ok(!/roomy/.test(ctx._id('view').className), 'a transcript was given the wide layout');
});

// ---------- the stylesheet ----------

const CSS = fs.readFileSync(path.join(WEB, 'style.css'), 'utf8');

// block returns the body of the first @media block whose query contains `q`.
function block(q) {
  const at = CSS.indexOf('@media ' + q);
  assert.ok(at >= 0, 'no @media block for ' + q);
  const start = CSS.indexOf('{', at);
  let depth = 0;
  for (let i = start; i < CSS.length; i++) {
    if (CSS[i] === '{') depth++;
    else if (CSS[i] === '}' && --depth === 0) return CSS.slice(start + 1, i);
  }
  assert.fail('unclosed @media block for ' + q);
}

// rule returns the declarations of the first `sel { ... }` rule in `css`. The
// selector is matched at the start of a line, which is where every rule in the
// stylesheet begins.
function rule(css, sel) {
  const esc = sel.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = css.match(new RegExp('^[ \\t]*' + esc + '\\s*\\{([^}]*)\\}', 'm'));
  assert.ok(m, 'no rule for ' + sel);
  return m[1];
}

test('the rail is an overlay by default and a column once there is room', () => {
  assert.match(rule(CSS, '.side'), /position:\s*fixed/);
  assert.match(rule(CSS, '.side'), /transform:\s*translateX\(-101%\)/);
  assert.match(rule(CSS, 'body.drawer .side'), /transform:\s*none/);

  const wide = block('(min-width: 900px)');
  assert.match(rule(wide, '#shell'), /grid-template-columns:\s*var\(--rail\)/);
  assert.match(rule(wide, '.side'), /position:\s*static/);
  assert.match(rule(wide, '.side'), /transform:\s*none/);
  // With the rail on screen the pane needs no way back, and the drawer has no
  // handle because there is no drawer.
  assert.match(rule(wide, '#menu, #side-close'), /display:\s*none/);
  assert.match(rule(wide, '#back'), /display:\s*none/);
});

// A hidden control has to be gone: the author stylesheet sets display on the
// header buttons, and that outranks the browser's own rule for [hidden].
test('a hidden control is hidden whatever else the stylesheet says', () => {
  assert.match(rule(CSS, '[hidden]'), /display:\s*none\s*!important/);
});

// Every screen is the same layout at a different size, so nothing may be laid
// out for one width alone.
test('the layout is fluid: no fixed page width anywhere', () => {
  const fixed = CSS.match(/(^|[^-])width:\s*\d{3,}px/g) || [];
  assert.deepStrictEqual(fixed, [], 'a fixed page width was introduced: ' + fixed.join(', '));
  assert.match(rule(CSS, '.wide'), /max-width:\s*var\(--column\)/);
});

test('the interface respects a request for less motion', () => {
  const quiet = block('(prefers-reduced-motion: reduce)');
  assert.match(quiet, /animation-duration:\s*\.01ms\s*!important/);
  assert.match(quiet, /transition-duration:\s*\.01ms\s*!important/);
});

test('the interface is legible in both colour schemes', () => {
  const dark = block('(prefers-color-scheme: dark)');
  const tokens = (rule(CSS, ':root').match(/--[a-z0-9-]+:/g) || []).filter(t => !/--(r|r-sm|r-lg|rail|column|mono|sans)\b/.test(t));
  for (const t of tokens) assert.ok(dark.includes(t), 'dark mode never redefines ' + t);
});
