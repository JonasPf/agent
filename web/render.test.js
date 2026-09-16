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
      if (method === 'GET') return { ok: true, status: 200, text: async () => '[]' };
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
  for (const f of ['markdown.js', 'transcript.js', 'notify.js', 'app.js']) {
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
  assert.match(b.innerHTML, /half/);
});

// findAll returns every descendant carrying the given class.
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
  id: '2C41F09B7DA35E86104B7', title: 'Greenhouse sensors', model: 'x/y', status: 'active',
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
  const rm = findAll(row, 'tag').find(t => t.textContent === 'delete');
  assert.ok(rm, 'the row offers no delete');
  ctx._confirm = true;
  await rm.onclick({ stopPropagation() {} });
  assert.ok(ctx._calls.some(c => c.method === 'DELETE' && c.path === '/sessions/2C41F09B7DA35E86104B7'),
    'the row did not delete the session: ' + JSON.stringify(ctx._calls));
});

test('a declined confirmation deletes nothing', async () => {
  const ctx = load();
  const row = ctx.sessionRow(session({ id: '2C41F09B7DA35E86104B7' }));
  const rm = findAll(row, 'tag').find(t => t.textContent === 'delete');
  ctx._confirm = false;
  await rm.onclick({ stopPropagation() {} });
  assert.ok(!(ctx._calls || []).some(c => c.method === 'DELETE'),
    'a declined confirmation still deleted: ' + JSON.stringify(ctx._calls));
});

test('the row still shows how much disk the session holds', () => {
  const ctx = load();
  const row = ctx.sessionRow(session({ disk_bytes: 2048 }));
  assert.match(find(row, 's').textContent, /2\.0 kB/);
});

// allText is every piece of text under a node, in order.
const allText = n => [n.textContent, ...(n.children || []).map(allText)].join(' ');

const reach = over => Object.assign({
  enforced: true, network_enforced: true, read_write: ["this session's working directory"],
  read: ['/tools/web_browse', '/usr', '/etc'], tool_read: [], files: ['/dev/null'], ports: [443]
}, over);

// A path one tool asked for is a decision about that tool; the rest is the same
// on every card. Mixed into one list, the one that matters reads like the others.
test('the Tools screen sets what a tool asked for apart from what every tool gets', () => {
  const ctx = load();
  const box = ctx.reachList({ name: 'web_browse', reach: reach({
    read: ['/tools/web_browse', '/usr', '/etc', '/proc', '/sys'], tool_read: ['/proc', '/sys'] }) });
  const every = find(box, 'reach-every');
  const own = find(box, 'reach-own');
  assert.ok(every, 'no group for what every tool gets');
  assert.ok(own, 'no group for what this tool asked for');
  assert.match(allText(own), /\/proc, \/sys/);
  assert.doesNotMatch(allText(every), /\/proc|\/sys/);
  assert.match(allText(every), /\/tools\/web_browse, \/usr, \/etc/);
  assert.match(allText(every), /443/);
});

test('a tool that asks for nothing of its own says so', () => {
  const ctx = load();
  const box = ctx.reachList({ name: 'read', reach: reach() });
  assert.match(allText(find(box, 'reach-own')), /nothing beyond what every tool gets/);
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

// A tool that failed is already open at its error. The logs stay behind the
// toggle even then: the error is the answer, the logs are the evidence.
test('a failed call opens its error and still folds its logs away', () => {
  const ctx = load();
  const n = ctx.renderEntry(toolEntry({
    tool_result: { ok: false, error: 'connection refused' }, stderr: 'dial tcp: connect: refused' }));
  assert.strictEqual(find(n, 'logs').hidden, true);
});
