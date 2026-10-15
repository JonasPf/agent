'use strict';

// How a conversation's context is shown and managed: what a compaction leaves
// on screen, how one is asked for, where its thresholds are set, and how a
// person finds what it folded away. The pure layout is checked on its own; the
// rest drives the real functions in app.js against a DOM small enough to read.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');
const { transcriptRows, ftsQuery, snippetParts } = require('./transcript.js');

const WEB = __dirname;

function node(tag) {
  const n = {
    tagName: tag, children: [], style: {}, hidden: false, className: '', id: '',
    title: '', type: '', value: '', parent: null, _text: '', _html: null, dataset: {},
    get textContent() { return this._text; },
    set textContent(v) { this._text = String(v); this._html = null; this.children = []; },
    set innerHTML(v) { this._html = String(v); this.children = []; },
    get innerHTML() { return this._html == null ? this._text : this._html; },
    append(...k) {
      for (const x of k) {
        if (!x) continue;
        if (x.tagName === '#fragment') { for (const c of x.children) { c.parent = this; this.children.push(c); } }
        else { if (typeof x === 'object') x.parent = this; this.children.push(x); }
      }
    },
    appendChild(k) { k.parent = this; this.children.push(k); return k; },
    querySelector(sel) { return find(this, sel.replace(/^\./, '')); },
    insertAdjacentHTML(where, html) { this._html = (this._html == null ? '' : this._html) + String(html); },
    setAttribute() {}, focus(o) { this._focused = o || {}; }, addEventListener() {},
    scrollIntoView(o) { this._scrolledInto = o || {}; },
    replaceWith(x) {
      if (!this.parent) return;
      const kids = this.parent.children;
      kids[kids.indexOf(this)] = x;
      x.parent = this.parent;
    },
    remove() {
      if (!this.parent) return;
      this.parent.children = this.parent.children.filter(c => c !== this);
      this.parent = null;
    },
    scrollHeight: 0, scrollTop: 0, clientHeight: 0
  };
  return n;
}

const classes = n => String(n.className || '').split(/\s+/);

function find(n, cls) {
  for (const c of n.children || []) {
    if (classes(c).includes(cls)) return c;
    const deeper = find(c, cls);
    if (deeper) return deeper;
  }
  return null;
}

function findAll(n, cls, out) {
  out = out || [];
  for (const c of n.children || []) {
    if (classes(c).includes(cls)) out.push(c);
    findAll(c, cls, out);
  }
  return out;
}

function textOf(n) {
  let out = String(n._text || n.text || '') + ' ' + String(n._html == null ? '' : n._html);
  for (const c of n.children || []) out += ' ' + textOf(c);
  return out;
}

// buttons returns every button under a node, by its label.
function button(n, label) {
  const all = [];
  (function walk(x) {
    for (const c of x.children || []) {
      if (c.tagName === 'button') all.push(c);
      walk(c);
    }
  })(n);
  return all.find(b => b.textContent === label);
}

const session = over => Object.assign({
  id: 'S1', title: 'Boiler', model: 'x/y', status: 'active', entry_count: 3,
  context_used: 30000, compact_at_tokens: 40000, keep_verbatim_tokens: 10000, cost: 0, disk_bytes: 0,
  last_active_at: new Date().toISOString(), unread: 0
}, over);

// load boots app.js with GETs served from `routes`. A write answers from
// `routes[method + ' ' + path]`; one carrying `status` answers with that status.
function load(routes) {
  const ids = {};
  const ctx = {
    console, clearTimeout, requestAnimationFrame: f => f(),
    setTimeout: (f, ms) => { const h = setTimeout(f, ms); if (h && h.unref) h.unref(); return h; },
    setInterval: () => 0, clearInterval: () => {},
    document: {
      createElement: node, createTextNode: t => ({ text: t, textContent: t }),
      createDocumentFragment: () => node('#fragment'),
      getElementById: id => ids[id] || (ids[id] = node('div')),
      body: node('body'), addEventListener() {}, hidden: false
    },
    location: { hash: '', protocol: 'http:', host: 'x', replace(h) { this.hash = h; }, reload() {} },
    history: { length: 1 },
    navigator: {},
    fetch: async (p, opts) => {
      const method = (opts || {}).method || 'GET';
      (ctx._calls = ctx._calls || []).push({ path: p, method, body: (opts || {}).body });
      if (method !== 'GET') {
        const reply = (routes || {})[method + ' ' + p];
        if (reply == null) return { ok: true, status: 204, text: async () => '' };
        const status = reply.status || 200;
        return { ok: status < 300, status, text: async () => JSON.stringify(reply.body || reply) };
      }
      const body = (routes || {})[p];
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
  for (const f of ['markdown.js', 'transcript.js', 'notify.js', 'models.js', 'app.js']) {
    let src = fs.readFileSync(path.join(WEB, f), 'utf8');
    if (f === 'app.js') src = src.replace(/^boot\(\);$/m, '');
    vm.runInContext(src, ctx, { filename: f });
  }
  ctx._id = id => ids[id] || (ids[id] = node('div'));
  return ctx;
}

const at = '2026-10-15T10:00:00Z';
const m = (seq, role, text) => ({ seq, type: 'message', role, text, created_at: at });

// A compacted conversation as it lies on disk: the prompt, the turns folded
// away, an older compaction among them, the turns kept word for word, the
// compaction written after those, and what was said since.
const compacted = [
  { seq: 0, type: 'prompt', sections: [{ name: 'persona', text: 'PERSONA', tokens: 1 }], text: 'system prompt', created_at: at },
  m(1, 'user', 'FOLDED question'),
  m(2, 'assistant', 'FOLDED answer'),
  { seq: 3, type: 'compaction', covers_through: 0, text: 'OLDER SUMMARY', created_at: at },
  m(4, 'user', 'KEPT question'),
  m(5, 'assistant', 'KEPT answer'),
  { seq: 6, type: 'compaction', covers_through: 3, text: 'NEWEST SUMMARY', folded_turns: 1,
    tokens_before: 41000, tokens_after: 9000, created_at: at },
  m(7, 'user', 'NEW question'),
];

// ---- the layout, pure ----

test('a conversation that never compacted is shown whole, in order', () => {
  const entries = [compacted[0], m(1, 'user', 'a'), m(2, 'assistant', 'b')];
  const rows = transcriptRows(entries);
  assert.deepStrictEqual(rows.map(r => [r.entry.seq, r.part]), [[0, 'live'], [1, 'live'], [2, 'live']]);
});

// What is on screen is what the agent reads, in the order it reads it: the
// prompt, the summary, the turns carried over, and what came since.
test('a compacted conversation is shown the way the agent reads it', () => {
  const rows = transcriptRows(compacted);
  assert.deepStrictEqual(rows.map(r => [r.entry.seq, r.part]),
    [[0, 'live'], [6, 'summary'], [4, 'carried'], [5, 'carried'], [7, 'live']]);
});

// A fork says once where it came from, in the event that seeded it. That event
// is the fork's own, written before the copy, so a compaction the copy carries
// covers it by position without standing for it: it is drawn after the prompt
// whatever the compaction covers, and never more than once.
test('a compacted fork still says where it was copied from', () => {
  const fork = [
    compacted[0],
    { seq: 1, type: 'event', event_kind: 'forked_from', carried_from: 'ORIGIN', text: 'copied from ORIGIN (fork): 8 entries', created_at: at },
    m(2, 'user', 'FOLDED question'),
    m(3, 'assistant', 'FOLDED answer'),
    m(4, 'user', 'KEPT question'),
    m(5, 'assistant', 'KEPT answer'),
    { seq: 6, type: 'compaction', covers_through: 3, text: 'SUMMARY', created_at: at },
  ];
  const rows = transcriptRows(fork);
  assert.deepStrictEqual(rows.map(r => [r.entry.seq, r.part]),
    [[0, 'live'], [1, 'live'], [6, 'summary'], [4, 'carried'], [5, 'carried']]);
});

// ---- the search box ----

test('what a person types is searched as words, each matching as a prefix', () => {
  assert.strictEqual(ftsQuery('boiler press'), '"boiler"* "press"*');
  assert.strictEqual(ftsQuery('  "AND" OR - x(y) '), '"AND"* "OR"* "x(y)"*');
  assert.strictEqual(ftsQuery('   '), '');
});

test('a snippet marks what matched and nothing else', () => {
  assert.deepStrictEqual(snippetParts('…the [boiler] was [low]'), [
    { text: '…the ', hit: false }, { text: 'boiler', hit: true }, { text: ' was ', hit: false }, { text: 'low', hit: true }]);
});

// ---- the conversation ----

function conversation(sess, entries, extra) {
  const ctx = load(Object.assign({ '/sessions/S1': { session: sess }, '/sessions/S1/transcript': entries }, extra));
  ctx.location.hash = '#session/S1';
  vm.runInContext("state.view = 'session'; state.arg = 'S1';", ctx);
  return ctx;
}

test('what a compaction folded is not on screen, and nothing offers to reveal it', async () => {
  const ctx = conversation(session(), compacted);
  await ctx.viewSession(node('div'));
  const t = ctx._id('transcript');
  const text = textOf(t);
  assert.doesNotMatch(text, /FOLDED/, 'a folded turn is still drawn');
  assert.doesNotMatch(text, /OLDER SUMMARY/, 'a superseded summary is still drawn');
  assert.doesNotMatch(text, /hidden turns|Show \d+/i, 'the conversation still offers to reveal folded turns');
});

test('the summary stands ahead of the turns it carried over, and those are marked', async () => {
  const ctx = conversation(session(), compacted);
  await ctx.viewSession(node('div'));
  const t = ctx._id('transcript');
  const kids = t.children;
  const banner = kids.findIndex(k => classes(k).includes('compaction'));
  const kept = kids.findIndex(k => classes(k).includes('verbatim'));
  const fresh = kids.findIndex(k => /NEW question/.test(textOf(k)));
  assert.ok(banner >= 0, 'no compaction banner');
  assert.ok(kept > banner, 'the carried-over turns are not after the summary');
  assert.ok(fresh > kept, 'what came since is not after the carried-over turns');
  const group = kids[kept];
  assert.match(textOf(group), /KEPT question/);
  assert.match(textOf(group), /KEPT answer/);
  assert.match(textOf(group), /word for word/i, 'the carried-over turns do not say what they are');
  assert.match(textOf(kids[banner]), /NEWEST SUMMARY/);
  assert.match(textOf(kids[banner]), /41,000 → 9,000/);
  assert.match(textOf(kids[0]), /prompt[\s\S]*persona/, 'the prompt is no longer at the top');
});

test('the summary offers a search of what it folded away', async () => {
  const ctx = conversation(session(), compacted);
  await ctx.viewSession(node('div'));
  const banner = find(ctx._id('transcript'), 'compaction');
  const search = button(banner, 'Search earlier messages');
  assert.ok(search, 'the banner offers no search');
  search.onclick();
  assert.strictEqual(ctx.location.hash, '#search/S1');
});

test('the conversation header compacts now', async () => {
  const ctx = conversation(session(), compacted, { 'POST /sessions/S1/compact': { session: session() } });
  await ctx.viewSession(node('div'));
  const compact = button(ctx._id('head-acts'), 'Compact');
  assert.ok(compact, 'the header offers no compaction');
  ctx._confirm = true;
  await compact.onclick();
  assert.ok(ctx._calls.some(c => c.method === 'POST' && c.path === '/sessions/S1/compact'),
    'compacting asked nothing of the agent: ' + JSON.stringify(ctx._calls));
});

test('the status line offers no compaction of its own', async () => {
  const ctx = conversation(session(), compacted);
  await ctx.viewSession(node('div'));
  assert.ok(!button(ctx._id('statusline'), 'compact'), 'the status line repeats the header\'s Compact');
});

test('a compaction refused says why', async () => {
  const ctx = conversation(session(), compacted, {
    'POST /sessions/S1/compact': { status: 409, body: { error: 'nothing to compact: the whole conversation fits' } } });
  await ctx.viewSession(node('div'));
  await button(ctx._id('head-acts'), 'Compact').onclick();
  assert.match(textOf(ctx.document.body), /nothing to compact/);
});

// ---- the details screen ----

test('the details screen sets when this conversation compacts', async () => {
  const ctx = load({ '/sessions/S1': { session: session() } });
  vm.runInContext("state.view = 'settings'; state.arg = 'S1';", ctx);
  const v = node('div');
  await ctx.viewSettings(v);
  const at = find(v, 'compact-at');
  const keep = find(v, 'keep-verbatim');
  assert.ok(at && keep, 'the details screen has no thresholds');
  assert.strictEqual(String(at.value), '40000');
  assert.strictEqual(String(keep.value), '10000');
  at.value = '80000';
  keep.value = '16000';
  await button(v, 'Save').onclick();
  const call = ctx._calls.find(c => c.method === 'PATCH' && c.path === '/sessions/S1');
  assert.ok(call, 'saving sent nothing');
  assert.deepStrictEqual(JSON.parse(call.body), { compact_at_tokens: 80000, keep_verbatim_tokens: 16000 });
});

// ---- search and history ----

const hits = [
  { session_id: 'S1', title: 'Boiler', seq: 1, snippet: 'the [boiler] pressure', role: 'user', created_at: at, compacted: true },
  { session_id: 'S1', title: 'Boiler', seq: 7, snippet: '[boiler] fixed', role: 'assistant', created_at: at, compacted: false },
];

test('search from a conversation looks in that conversation, and opens a hit where it was said', async () => {
  const q = '/search?q=' + encodeURIComponent('"boiler"*') + '&session=S1';
  const ctx = load({ '/sessions/S1': { session: session() }, [q]: hits });
  vm.runInContext("state.view = 'search'; state.arg = 'S1';", ctx);
  const v = node('div');
  await ctx.viewSearch(v);
  const input = find(v, 'text');
  input.value = 'boiler';
  await input.onchange();
  assert.ok(ctx._calls.some(c => c.path === q), 'the search did not ask for this conversation: ' + JSON.stringify(ctx._calls));
  const rows = findAll(v, 'hit-row');
  assert.strictEqual(rows.length, 2);
  assert.match(textOf(rows[0]), /compacted/, 'a folded hit does not say the agent no longer sees it');
  assert.doesNotMatch(textOf(rows[1]), /compacted/);
  assert.strictEqual(findAll(rows[0], 'match')[0].textContent, 'boiler');
  rows[0].onclick();
  assert.strictEqual(ctx.location.hash, '#history/S1/1');
});

test('the history shows everything stored, folded turns marked, and the hit picked out', async () => {
  const ctx = load({ '/sessions/S1': { session: session() }, '/sessions/S1/transcript': compacted });
  vm.runInContext("state.view = 'history'; state.arg = 'S1'; state.arg2 = '1';", ctx);
  const v = node('div');
  await ctx.viewHistory(v);
  const text = textOf(v);
  for (const said of ['FOLDED question', 'KEPT question', 'NEW question']) assert.match(text, new RegExp(said));
  const folded = findAll(v, 'folded').map(textOf).join(' ');
  assert.match(folded, /FOLDED question/, 'a folded turn is not marked as folded');
  assert.doesNotMatch(folded, /KEPT question|NEW question/, 'a turn the agent still sees is marked folded');
  const hit = find(v, 'hit');
  assert.ok(hit, 'the hit is not picked out');
  assert.match(textOf(hit), /FOLDED question/);
  assert.ok(hit._scrolledInto, 'the history did not open at the hit');
  assert.ok(!button(v, 'Edit'), 'the history offers to edit a summary');
});
