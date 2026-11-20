'use strict';

// These drive the real renderEntry() in app.js against a DOM, so they cover the
// path a transcript entry actually takes: entry → bubble → markup on the page.
// markdown.test.js checks the renderer alone; this checks that the transcript is
// wired to it, and that what the operator typed is still shown as they typed it.

const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const vm = require('vm');
const path = require('path');

const WEB = __dirname;

// A DOM small enough to read and complete enough to serialise: the assertions
// below are made against the markup the page would actually put on screen.
function node(tag) {
  const n = {
    tagName: tag, children: [], style: {}, hidden: false, className: '', id: '',
    title: '', _text: '', _html: null,
    get textContent() { return this._text; },
    set textContent(v) { this._text = String(v); this._html = null; this.children = []; },
    set innerHTML(v) { this._html = String(v); this.children = []; },
    get innerHTML() { return this._html == null ? this._text : this._html; },
    // A fragment is not itself inserted: appending one moves its children, and a
    // shim that keeps the wrapper would hide a real nesting mistake.
    append(...k) {
      for (const x of k) {
        if (!x) continue;
        if (x.tagName === '#fragment') this.children.push(...x.children);
        else this.children.push(x);
      }
    },
    appendChild(k) { this.children.push(k); return k; },
    querySelector(sel) { return find(this, sel.replace(/^\./, '')); },
    insertAdjacentHTML(where, html) { this._html = (this._html == null ? '' : this._html) + String(html); },
    setAttribute(k, v) { (this._attrs = this._attrs || {})[k] = String(v); },
    getAttribute(k) { return (this._attrs || {})[k] ?? null; },
    remove() {}, addEventListener() {},
    scrollHeight: 0, scrollTop: 0, clientHeight: 0
  };
  return n;
}

// find returns the first descendant carrying the given class.
function find(n, cls) {
  for (const c of n.children || []) {
    if (String(c.className || '').split(/\s+/).includes(cls)) return c;
    const deeper = find(c, cls);
    if (deeper) return deeper;
  }
  return null;
}

function load() {
  const ids = {};
  const ctx = {
    console, setTimeout, clearTimeout, requestAnimationFrame: f => f(),
    document: {
      createElement: node, createTextNode: t => ({ text: t, textContent: t }),
      createDocumentFragment: () => node('#fragment'),
      getElementById: id => ids[id] || (ids[id] = node('div')),
      body: node('body'), addEventListener() {}, hidden: false
    },
    location: { hash: '#session/S1', protocol: 'http:', host: 'x' },
    navigator: {},
    fetch: async (path, opts) => {
      const method = (opts || {}).method || 'GET';
      (ctx._calls = ctx._calls || []).push({ path, method });
      const body = (ctx._get || {})[path];
      if (method === 'GET') return { ok: true, status: 200, text: async () => body ? JSON.stringify(body) : '[]' };
      return { ok: true, status: 204, text: async () => '' };
    },
    confirm: () => ctx._confirm !== false,
    WebSocket: function () { return {}; }
  };
  ctx.Notification = function () {};
  ctx.Notification.permission = 'default';
  ctx.addEventListener = () => {};
  ctx.window = ctx;
  vm.createContext(ctx);
  for (const f of ['markdown.js', 'transcript.js', 'notify.js', 'models.js', 'apphost.js', 'app.js']) {
    let src = fs.readFileSync(path.join(WEB, f), 'utf8');
    if (f === 'app.js') src = src.replace(/^boot\(\);$/m, '');
    vm.runInContext(src, ctx, { filename: f });
  }
  return ctx;
}

const message = over => Object.assign({ type: 'message', role: 'assistant', created_at: new Date().toISOString() }, over);

// bub returns the bubble the page built for an entry, as markup.
function bub(ctx, entry) {
  const n = ctx.renderEntry(entry);
  const b = find(n, 'bub');
  assert.ok(b, 'the entry rendered no bubble');
  return b;
}

test('the page loads the renderer the transcript needs', () => {
  const html = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');
  assert.match(html, /<script src="\/markdown\.js"><\/script>/);
  assert.ok(html.indexOf('markdown.js') < html.indexOf('app.js'), 'markdown.js must load before app.js');
});

test('an agent message renders its markdown', () => {
  const ctx = load();
  const b = bub(ctx, message({ text: '## Done\n\n- **one**\n- `two`\n\nSee [docs](https://example.com).' }));
  assert.match(b.innerHTML, /<h2>Done<\/h2>/);
  assert.match(b.innerHTML, /<li><strong>one<\/strong><\/li>/);
  assert.match(b.innerHTML, /<li><code>two<\/code><\/li>/);
  assert.match(b.innerHTML, /<a href="https:\/\/example\.com"/);
});

test('a fenced block in an agent message becomes a code block', () => {
  const ctx = load();
  const b = bub(ctx, message({ text: 'run it:\n\n```sh\ntask check\n```' }));
  assert.match(b.innerHTML, /<pre data-lang="sh"><code>task check<\/code><\/pre>/);
});

test('markup in an agent message is shown, not run', () => {
  const ctx = load();
  const b = bub(ctx, message({ text: 'careful: <img src=x onerror=alert(1)>' }));
  assert.ok(!/<img/.test(b.innerHTML), b.innerHTML);
  assert.match(b.innerHTML, /&lt;img/);
});

// The operator's own words are not the model's output, and rewriting them under
// the operator would be the interface editing what they said.
test('the operator sees their own message exactly as typed', () => {
  const ctx = load();
  const b = bub(ctx, message({ role: 'user', text: 'use **stars** and `ticks` literally' }));
  assert.strictEqual(b.textContent, 'use **stars** and `ticks` literally');
  assert.ok(!/<strong>/.test(b.innerHTML), b.innerHTML);
});

test("a job's wake is shown as its prompt, not as prose", () => {
  const ctx = load();
  const b = bub(ctx, message({ role: 'user', job_id: 'job-123456', text: '# check the deploy' }));
  assert.strictEqual(b.textContent, '# check the deploy');
  assert.ok(!/<h1>/.test(b.innerHTML), b.innerHTML);
});

// A turn arrives token by token; it must read as the answer while it lands, not
// snap from source to rendered at the end.
test('a streaming turn renders as markdown while it arrives', () => {
  const ctx = load();
  vm.runInContext("state.streaming = '**half';", ctx);
  const n = ctx.streamNode();
  const b = find(n, 'bub');
  assert.ok(String(b.className).split(/\s+/).includes('md'));
  // Drawn in two parts — what has settled, and the block still arriving.
  const markup = x => String(x._html || '') + (x.children || []).map(markup).join('');
  assert.match(markup(b), /<strong>half|\*\*half/);
});

// findAll returns every descendant carrying the given class.
// Everything a node and its descendants would put on screen, as one string.
function textOf(n) {
  let out = String(n._text || '');
  for (const c of n.children || []) out += ' ' + textOf(c);
  return out;
}

function findAll(n, cls, out) {
  out = out || [];
  for (const c of n.children || []) {
    if (String(c.className || '').split(/\s+/).includes(cls)) out.push(c);
    findAll(c, cls, out);
  }
  return out;
}

// A carried message used to wear a "carried from" pill. The seeding event at the
// top of the successor already says where the conversation came from, and saying
// it again on every bubble is thirteen copies of one fact.
test('a carried message wears no attribution label', () => {
  const ctx = load();
  const n = ctx.renderEntry(message({ text: 'Earlier turn.', carried_from: '2C41F09B7DA35E86104B7' }));
  assert.strictEqual(findAll(n, 'attr').length, 0, 'the carried label is still rendered');
  assert.ok(String(n.className).split(/\s+/).includes('carried'), 'carried styling was lost too');
});

// The identifier in a seeding or rotation line is the only route to the session
// it names, and it is twenty-one characters to retype.
test('a session id in an event is a link to that session', () => {
  const ctx = load();
  const n = ctx.renderEntry({
    type: 'event', event_kind: 'carried_over', created_at: new Date().toISOString(),
    text: 'seeded from 2C41F09B7DA35E86104B7 (size): 13 carried messages'
  });
  const links = findAll(n, 'sid');
  assert.strictEqual(links.length, 1, 'the identifier was not linked');
  assert.strictEqual(links[0].href, '#session/2C41F09B7DA35E86104B7');
  assert.strictEqual(links[0].textContent, '2C41F09B7DA35E86104B7');
});

test('an event with no identifier renders its text unchanged', () => {
  const ctx = load();
  const n = ctx.renderEntry({
    type: 'event', event_kind: 'job_error', created_at: new Date().toISOString(),
    text: 'the check exited 1'
  });
  assert.strictEqual(findAll(n, 'sid').length, 0);
  const detail = find(n, 'detail');
  const text = (detail.children || []).map(c => c.textContent || c.text || '').join('');
  assert.match(text, /the check exited 1/);
});

const session = over => Object.assign({
  id: '2C41F09B7DA35E86104B7', title: 'Greenhouse sensors', model: 'x/y',
  entry_count: 3, context_used: 10, compact_at_tokens: 40000, cost: 0,
  disk_bytes: 0, last_active_at: new Date().toISOString(), unread: 0
}, over);

// The list is read newest-first: what the operator was last doing is what they
// are most likely looking for.
test('sessions are ordered by recency by default', () => {
  const ctx = load();
  const out = ctx.sortSessions([
    session({ id: 'A', last_active_at: '2026-09-01T00:00:00Z', disk_bytes: 900 }),
    session({ id: 'B', last_active_at: '2026-09-03T00:00:00Z', disk_bytes: 1 }),
    session({ id: 'C', last_active_at: '2026-09-02T00:00:00Z', disk_bytes: 500 }),
  ], 'recent');
  assert.deepStrictEqual(out.map(s => s.id), ['B', 'C', 'A']);
});

// Rotation copies a session's working directory into its successor, so disk use
// accumulates down a chain. Ordering by size is how the operator finds what is
// worth deleting; recency cannot show it.
test('sessions can be ordered by size, largest first', () => {
  const ctx = load();
  const out = ctx.sortSessions([
    session({ id: 'A', last_active_at: '2026-09-01T00:00:00Z', disk_bytes: 900 }),
    session({ id: 'B', last_active_at: '2026-09-03T00:00:00Z', disk_bytes: 1 }),
    session({ id: 'C', last_active_at: '2026-09-02T00:00:00Z', disk_bytes: 500 }),
  ], 'size');
  assert.deepStrictEqual(out.map(s => s.id), ['A', 'C', 'B']);
});

test('sorting does not mutate the list it was given', () => {
  const ctx = load();
  const list = [session({ id: 'A', disk_bytes: 1 }), session({ id: 'B', disk_bytes: 9 })];
  ctx.sortSessions(list, 'size');
  assert.deepStrictEqual(list.map(s => s.id), ['A', 'B']);
});

test('a session with no recorded size sorts as empty rather than vanishing', () => {
  const ctx = load();
  const out = ctx.sortSessions([session({ id: 'A' }), session({ id: 'B', disk_bytes: 5 })], 'size');
  assert.deepStrictEqual(out.map(s => s.id), ['B', 'A']);
});

// Deleting is the point of showing the size, so it has to be reachable from the
// row that shows it rather than three screens in.
test('a session row offers a delete that asks first', async () => {
  const ctx = load();
  const row = ctx.sessionRow(session({ id: '2C41F09B7DA35E86104B7', disk_bytes: 2048 }));
  const rm = find(row, 'row-del');
  assert.ok(rm, 'the row offers no delete');
  ctx._confirm = true;
  await rm.onclick({ stopPropagation() {} });
  assert.ok(ctx._calls.some(c => c.method === 'DELETE' && c.path === '/sessions/2C41F09B7DA35E86104B7'),
    'the row did not delete the session: ' + JSON.stringify(ctx._calls));
});

test('a declined confirmation deletes nothing', async () => {
  const ctx = load();
  const row = ctx.sessionRow(session({ id: '2C41F09B7DA35E86104B7' }));
  const rm = find(row, 'row-del');
  ctx._confirm = false;
  await rm.onclick({ stopPropagation() {} });
  assert.ok(!(ctx._calls || []).some(c => c.method === 'DELETE'),
    'a declined confirmation still deleted: ' + JSON.stringify(ctx._calls));
});

test('the row still shows how much disk the session holds', () => {
  const ctx = load();
  const row = ctx.sessionRow(session({ disk_bytes: 2048 }));
  assert.match(textOf(find(row, 's')), /2\.0 kB/);
});

// A row is a title and one line under it (webui.html, "Session management"):
// when it last moved, what it holds, what it cost, and its model, with jobs and
// what is unread said in the accent. Three grey lines a row were read as none.
test('a list row says what matters in one line', () => {
  const ctx = load();
  const row = ctx.sessionRow(session({ disk_bytes: 2048, cost: 0.0032, model: 'deepseek/deepseek-v4.1-flash',
    job_count: 1, unread: 2, entry_count: 7, context_used: 443, compact_at_tokens: 80000 }));
  const lines = findAll(row, 's');
  assert.strictEqual(lines.length, 1, 'a row has ' + lines.length + ' lines under its title');
  const line = textOf(lines[0]);
  for (const re of [/2\.0 kB/, /\$0\.0032/, /deepseek-v4\.1-flash/, /1 job/, /2 unread/]) assert.match(line, re);
  assert.doesNotMatch(line, /entries|tok\b/, 'the row still carries what belongs in its details');
  assert.match(find(row, 'row-item').className, /\bunread\b/, 'a row with unread messages is not marked');
});

// Delete is behind a swipe to the left on a touch screen, so it does not sit on
// every row competing with the title, and a tap that ends a swipe does not
// also open the conversation.
test('a row swiped left offers its delete, and the tap ending the swipe opens nothing', () => {
  const ctx = load();
  const wrap = ctx.sessionRow(session({ id: 'S7' }));
  const row = find(wrap, 'row-item');
  const at = (x, y) => ({ clientX: x, clientY: y || 10, pointerId: 1, pointerType: 'touch', preventDefault() {} });
  row.onpointerdown(at(300));
  row.onpointermove(at(250));
  row.onpointermove(at(180));
  row.onpointerup(at(180));
  assert.match(wrap.className, /\bopen\b/, 'a swipe to the left did not uncover the delete');
  // Some browsers send a click where a swipe ends; it neither opens the
  // conversation nor shuts what the swipe uncovered.
  row.onclick();
  assert.notStrictEqual(ctx.location.hash, '#session/S7', 'the click ending a swipe opened the conversation');
  assert.match(wrap.className, /\bopen\b/, 'the click ending a swipe shut the row again');
  row.onpointerdown(at(200));
  row.onpointerup(at(200));
  row.onclick();
  assert.notStrictEqual(ctx.location.hash, '#session/S7', 'a tap on the slid row opened the conversation');
  assert.doesNotMatch(wrap.className, /\bopen\b/, 'a tap on the slid row did not shut it');
  row.onpointerdown(at(200));
  row.onpointerup(at(200));
  row.onclick();
  assert.strictEqual(ctx.location.hash, '#session/S7', 'a plain tap does not open the conversation');
});

test('a row moved up or down is scrolled, not swiped', () => {
  const ctx = load();
  const wrap = ctx.sessionRow(session({ id: 'S7' }));
  const row = find(wrap, 'row-item');
  const at = (x, y) => ({ clientX: x, clientY: y, pointerId: 1, pointerType: 'touch', preventDefault() {} });
  row.onpointerdown(at(300, 100));
  row.onpointermove(at(280, 180));
  row.onpointerup(at(280, 180));
  assert.doesNotMatch(wrap.className, /\bopen\b/, 'a scroll uncovered the delete');
});

// ---- tool logs ----

// What a tool wrote to standard error has always been kept with its result and
// never shown. It belongs beside the call it explains — but an operator reading
// a conversation is not debugging it, until they are, so it waits behind
// something small enough to ignore.

const toolEntry = over => Object.assign({
  type: 'message', role: 'tool', tool_name: 'web_fetch', tool_call_id: 'c1',
  tool_result: { ok: true, content: 'the page' }, created_at: new Date().toISOString()
}, over);

test('a tool call that logged offers its logs, closed', () => {
  const ctx = load();
  const n = ctx.renderEntry(toolEntry({ stderr: 'chromium: dbus not available' }));
  assert.ok(findAll(n, 'logtoggle').length === 1, 'a call with logs offers no way to see them');
  const pre = find(n, 'logs');
  assert.ok(pre, 'the logs were not rendered at all');
  assert.strictEqual(pre.hidden, true, 'the logs are open by default and crowd out the conversation');
  assert.match(pre.textContent, /dbus not available/);
});

test('opening the logs shows them', () => {
  const ctx = load();
  const n = ctx.renderEntry(toolEntry({ stderr: 'chromium: dbus not available' }));
  findAll(n, 'logtoggle')[0].onclick();
  assert.strictEqual(find(n, 'logs').hidden, false, 'the toggle did not open the logs');
});

// A toggle that opens nothing is a promise of detail that is not there.
test('a tool call that logged nothing offers no toggle', () => {
  const ctx = load();
  const n = ctx.renderEntry(toolEntry({}));
  assert.strictEqual(findAll(n, 'logtoggle').length, 0, 'a toggle appeared with no logs behind it');
  assert.strictEqual(findAll(n, 'logs').length, 0);
});

// A screen a tool opened stays in sight when the result is folded, and whether
// or not the agent repeats the link in its reply.
test('a tool result that opens a screen offers it', () => {
  const ctx = load();
  const n = ctx.renderEntry(toolEntry({ tool_name: 'ask_operator', tool_result: { ok: true,
    content: 'Ready. Put this link in your reply: [Open](#toolpanel/ask_operator/S1/20261031-1200-ab)' } }));
  const open = findAll(n, 'btn');
  assert.strictEqual(open.length, 1);
  assert.strictEqual(open[0].href, '#toolpanel/ask_operator/S1/20261031-1200-ab');
});

test('a tool result with no screen offers none', () => {
  const ctx = load();
  assert.strictEqual(findAll(ctx.renderEntry(toolEntry({})), 'btn').length, 0);
});

// A tool that failed is already open at its error. The logs stay behind the
// toggle even then: the error is the answer, the logs are the evidence.
test('a failed call opens its error and still folds its logs away', () => {
  const ctx = load();
  const n = ctx.renderEntry(toolEntry({
    tool_result: { ok: false, error: 'connection refused' }, stderr: 'dial tcp: connect: refused' }));
  assert.strictEqual(find(n, 'logs').hidden, true);
});

// A page stays open for days, and the gateway's prices move under it. The model
// named on a configuration is priced from the catalogue as it is now, the same
// figures the model dialog shows, not from the copy the page loaded first.
test("a configuration's model is priced from the current catalogue", async () => {
  const ctx = load();
  vm.runInContext(`state.models = [{ id: 'z-ai/glm-5.3', name: 'Z.ai: GLM 5.3', prompt_price: 0.00000084, completion_price: 0.00000264 }]`, ctx);
  ctx._get = { '/models': [{ id: 'z-ai/glm-5.3', name: 'Z.ai: GLM 5.3', prompt_price: 0.0000014, completion_price: 0.0000044 }] };
  const v = ctx.document.createElement('div');
  await ctx.configFacts(v, { model: 'z-ai/glm-5.3' });
  const facts = find(v, 'facts');
  const text = JSON.stringify(facts);
  assert.match(text, /\$1\.40/);
  assert.match(text, /\$4\.40/);
  assert.ok(!/\$0\.84/.test(text), text);
});
