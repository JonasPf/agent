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
    title: '', type: '', value: '', parent: null, _text: '', _html: null, dataset: {},
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
  context_used: 10, compact_at_tokens: 40000, cost: 0, disk_bytes: 0,
  last_active_at: new Date().toISOString(), unread: 0
}, over);

// load boots app.js against a DOM small enough to read, with the pages the rail
// asks for served from `routes`.
function load(routes) {
  const ids = {};
  const ctx = {
    console, clearTimeout, requestAnimationFrame: f => f(),
    // A toast lingers for seconds; unref'd, its timer still fires while a test
    // waits and no longer holds the process open after the last one.
    setTimeout: (f, ms) => { const h = setTimeout(f, ms); if (h && h.unref) h.unref(); return h; },
    // A ticking clock would keep the test process alive; a test that needs the
    // tick calls the view again instead.
    setInterval: () => 0, clearInterval: () => {},
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
      (ctx._calls = ctx._calls || []).push({ path: p, method, body: (opts || {}).body });
      const body = (routes || {})[p];
      if (method !== 'GET') {
        // A write answers with nothing unless the test gave it a reply, keyed
        // by method and path: creating a session has to name the one created.
        const reply = (routes || {})[method + ' ' + p];
        if (reply == null) return { ok: true, status: 204, text: async () => '' };
        return { ok: true, status: 201, text: async () => JSON.stringify(reply) };
      }
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

// ---------- version ----------

// Everything the node and its descendants would put on screen, as one string:
// the panel's version line is a div among others and asserting on the whole
// screen is what says it is visible without opening anything.
function textOf(n) {
  let out = String(n._text || '') + ' ' + String(n._html == null ? '' : n._html);
  for (const c of n.children || []) out += ' ' + textOf(c);
  return out;
}

// Which build is answering is a question asked once, after an upgrade. It sits
// at the foot of the More panel, and the changelog stays folded until it is
// asked for, so neither is ever in the way of a conversation.
test('the More panel names the running build', async () => {
  const ctx = load({ '/version': { version: '7da7e74', built_at: '2026-09-11T10:00:00Z', changelog: '## 2026-09-11\n\n- Compact in place.\n' } });
  const v = ctx._id('view');
  await ctx.viewPanels(v);
  assert.match(textOf(v), /7da7e74/, 'the panel does not say which build it is');
});

test('the changelog is folded away until it is opened', async () => {
  const ctx = load({ '/version': { version: '7da7e74', built_at: '2026-09-11T10:00:00Z', changelog: '## 2026-09-11\n\n- Compact in place.\n' } });
  const v = ctx._id('view');
  await ctx.viewPanels(v);
  const log = find(v, 'changelog');
  assert.ok(log, 'no changelog on the panel');
  assert.strictEqual(log.hidden, true, 'the changelog is open before it was asked for');
  const toggle = findAll(v, 'btn').find(b => /changed/i.test(b.textContent || ''));
  assert.ok(toggle, 'nothing offers to show what changed');
  toggle.onclick();
  assert.strictEqual(log.hidden, false, 'asking for the changelog did not open it');
  assert.match(String(log.innerHTML), /Compact in place\./, 'the changelog is not rendered markdown');
});

// The changelog ships in the image. Its absence is a packaging mistake, and the
// version has to stay readable through it.
test('a build without a changelog still names itself', async () => {
  const ctx = load({ '/version': { version: '7da7e74', built_at: '', changelog: '' } });
  const v = ctx._id('view');
  await ctx.viewPanels(v);
  assert.match(textOf(v), /7da7e74/, 'the version went missing with the changelog');
  assert.strictEqual(find(v, 'changelog'), null, 'an empty changelog was rendered anyway');
});

// ---------- granted environment ----------

// The dialog a control opens is appended to the page, not to the view, so that
// it can cover the whole screen and slide in over it.
const openDialog = ctx => {
  const back = findAll(ctx.document.body, 'dialog-back').filter(d => String(d.className).includes('open'));
  return back.length ? back[back.length - 1] : null;
};

// What the page would POST when the configuration screen is confirmed.
async function startConversation(ctx, v) {
  const start = findAll(v, 'btn').find(b => /Start conversation/.test(b.textContent));
  assert.ok(start, 'no way to start the conversation');
  await start.onclick();
  const sent = ctx._calls.filter(c => c.method === 'POST' && c.path === '/sessions');
  assert.strictEqual(sent.length, 1, 'starting did not create exactly one session');
  return JSON.parse(sent[0].body);
}

// Enough of a new-conversation screen to drive: no catalogue, no tools, no
// skills, no environment, unless the test says otherwise.
function newScreen(routes) {
  return load(Object.assign({
    '/sessions': [], '/models': [], '/preferences': {}, '/env': [],
    '/tools': { tools: [] }, '/skills': { skills: [] },
    'POST /sessions': session({ id: 'NEW' }),
  }, routes));
}

const FABLE = {
  id: 'anthropic/claude-fable-5.1', name: 'Anthropic: Claude Fable 5.1', created: 300,
  context_length: 1000000, prompt_price: 0.00001, completion_price: 0.00005,
  intelligence_index: 53.4, coding_index: 81.6, agentic_index: 58,
};
const SMALL = {
  id: 'small/unmeasured', name: 'Small: Unmeasured', created: 400,
  context_length: 32000, prompt_price: 0.0000001, completion_price: 0.0000002,
};

// ---------- waiting, and copying ----------

function conversationScreen(sess, entries) {
  const ctx = load({ '/sessions/S1': { session: sess }, '/sessions/S1/transcript': entries || [] });
  ctx.location.hash = '#session/S1';
  vm.runInContext("state.view = 'session'; state.arg = 'S1';", ctx);
  return ctx;
}

// A turn can take a minute. Nothing on screen during it reads as stuck, so the
// status line says the agent is working and counts the seconds.
test('the status line counts how long the agent has been working', async () => {
  const ctx = conversationScreen(session({ id: 'S1', working_seconds: 7 }));
  await ctx.viewSession(node('div'));
  assert.match(textOf(ctx._id('statusline')), /working\s+7s/, 'a working session does not say how long it has been');

  ctx.handle({ kind: 'idle', session_id: 'S1' });
  assert.ok(!/working/.test(textOf(ctx._id('statusline'))), 'the indicator stayed after the agent finished');

  ctx.handle({ kind: 'working', session_id: 'S1' });
  assert.match(textOf(ctx._id('statusline')), /working\s+0s/, 'a turn that starts on screen does not show at once');
});

test('an idle conversation shows no indicator', async () => {
  const ctx = conversationScreen(session({ id: 'S1' }));
  await ctx.viewSession(node('div'));
  assert.ok(!/working/.test(textOf(ctx._id('statusline'))));
});

test('sending a message shows the indicator before the server answers', async () => {
  const ctx = conversationScreen(session({ id: 'S1' }));
  await ctx.viewSession(node('div'));
  const form = findAll(ctx._id('foot'), 'composer')[0];
  form.children.find(c => c.tagName === 'textarea').value = 'hello';
  await form.onsubmit({ preventDefault() {} });
  assert.match(textOf(ctx._id('statusline')), /working\s+0s/);
});

test('the whole conversation copies to the clipboard from its header', async () => {
  const ctx = conversationScreen(session({ id: 'S1', title: 'Greenhouse sensors' }), [
    { seq: 1, type: 'message', role: 'user', text: 'How warm is it?', created_at: '2026-09-15T10:01:00Z' },
    { seq: 2, type: 'message', role: 'assistant', text: '21.5 degrees.', created_at: '2026-09-15T10:01:05Z' },
  ]);
  let copied = null;
  ctx.navigator.clipboard = { writeText: async t => { copied = t; } };
  await ctx.viewSession(node('div'));
  const copy = ctx._id('head-acts').children.find(b => /copy/i.test(b.textContent));
  assert.ok(copy, 'the conversation header offers no copy');
  await copy.onclick();
  assert.ok(copied && copied.includes('How warm is it?') && copied.includes('21.5 degrees.'),
    'the clipboard does not hold the conversation: ' + copied);
  assert.ok(copied.includes('Greenhouse sensors'), 'the copy does not carry the title');
});

// ---------- what a tool may reach ----------

const REACH = {
  enforced: true, network_enforced: true,
  read_write: ['this session’s working directory', '/app/state/data/db'],
  read: ['/app/tools', '/usr', '/etc'], files: ['/dev/null'],
  ports: [22, 443, 8080, 45123], tool_api_port: 45123, operator_port: 7770,
};

// A tool's boundary is part of what the tool is, so the Tools screen says it:
// the files it may read and write, and the ports it may open.
test('the tools screen says what each tool may read, write, and connect to', async () => {
  const ctx = load({ '/tools': { failures: [], tools: [
    { name: 'bash', description: 'Run a command.', parameters: {}, reach: REACH },
    { name: 'reload_tools', description: 'Reload.', parameters: {}, builtin: true },
  ] } });
  const v = node('div');
  await ctx.viewTools(v);
  const rows = findAll(v, 'row-item');
  const bash = textOf(rows[0]);
  for (const want of ['working directory', '/app/state/data/db', '/usr', '/dev/null', '22', '443', '8080']) {
    assert.ok(bash.includes(want), `the bash row does not say ${want}: ${bash}`);
  }
  assert.match(bash, /7770/, 'the row does not say the operator port is out of reach');
  assert.match(bash, /tool API/, 'the row does not say how the tool reaches the agent');
  assert.match(textOf(rows[1]), /inside the agent/, 'a builtin does not say it runs inside the agent');
});

test('a boundary that is not enforced says so on the tool', async () => {
  const ctx = load({ '/tools': { failures: [], tools: [
    { name: 'bash', description: 'Run a command.', parameters: {}, reach: Object.assign({}, REACH,
      { enforced: false, reason: 'Landlock is a Linux facility', network_enforced: false, network_reason: 'Landlock is a Linux facility' }) },
  ] } });
  const v = node('div');
  await ctx.viewTools(v);
  const text = textOf(findAll(v, 'row-item')[0]);
  assert.match(text, /not enforced/i, 'an unenforced boundary is presented as though it holds');
});

// ---------- the model dialog ----------

test('the model is chosen in a dialog that says what each costs and how it measured', async () => {
  const ctx = newScreen({ '/models': [SMALL, FABLE], '/preferences': { model: SMALL.id } });
  const v = node('div');
  await ctx.viewNew(v);

  const choice = find(v, 'model-choice');
  assert.ok(choice, 'the model is not a control that opens a dialog');
  assert.strictEqual(openDialog(ctx), null, 'a dialog is open before anything was clicked');
  await choice.onclick();
  const dialog = openDialog(ctx);
  assert.ok(dialog, 'clicking the model opened no dialog');

  const rows = findAll(dialog, 'model-row');
  assert.deepStrictEqual(rows.map(r => find(r, 'n').textContent), ['Claude Fable 5.1', 'Unmeasured'],
    'the dialog does not rank the measured model first');
  const fable = textOf(rows[0]);
  for (const want of ['$10', '$50', '1M', '53.4', '81.6', '58']) {
    assert.ok(fable.includes(want), `the row does not say ${want}: ${fable}`);
  }
  const link = findAll(rows[0], 'openrouter')[0];
  assert.ok(link, 'the row has no link to OpenRouter');
  assert.strictEqual(link.href, 'https://openrouter.ai/anthropic/claude-fable-5.1');
  assert.strictEqual(link.target, '_blank');

  rows[0].onclick();
  assert.strictEqual(openDialog(ctx), null, 'choosing a model left the dialog open');
  assert.ok(textOf(find(v, 'model-choice')).includes('Claude Fable 5.1'), 'the chosen model is not shown on the screen');
  assert.strictEqual((await startConversation(ctx, v)).model, FABLE.id);
});

// The most recent conversation is not necessarily what the operator wants next:
// it may be a fork onto something experimental. The model last chosen is.
test('a new conversation is preselected on the model last chosen', async () => {
  const ctx = newScreen({
    '/sessions': [session({ model: 'y/most-recent' })],
    '/preferences': { model: 'x/last-chosen' },
  });
  const v = node('div');
  await ctx.viewNew(v);
  assert.ok(textOf(find(v, 'model-choice')).includes('x/last-chosen'),
    'the screen is not preselected on the model last chosen');
  assert.strictEqual((await startConversation(ctx, v)).model, 'x/last-chosen');
});

test('searching narrows the dialog, and the model already chosen stays in it', async () => {
  const ctx = newScreen({ '/models': [SMALL, FABLE], '/models?q=unmeasured': [SMALL], '/preferences': { model: FABLE.id } });
  const v = node('div');
  await ctx.viewNew(v);
  await find(v, 'model-choice').onclick();
  const dialog = openDialog(ctx);
  const search = findAll(dialog, 'text').find(n => n.type === 'search');
  assert.ok(search, 'the dialog has no search');
  search.value = 'unmeasured';
  search.oninput();
  await new Promise(r => setTimeout(r, 220));
  const ids = findAll(openDialog(ctx), 'model-row').map(r => r.dataset.id);
  assert.ok(ids.includes(SMALL.id), 'the match is not listed');
  assert.ok(ids.includes(FABLE.id), 'the chosen model vanished from a search that excluded it');
  assert.strictEqual(ids.length, 2);
});

test('the dialog can rank by price instead of intelligence', async () => {
  const ctx = newScreen({ '/models': [SMALL, FABLE], '/preferences': { model: SMALL.id } });
  const v = node('div');
  await ctx.viewNew(v);
  await find(v, 'model-choice').onclick();
  const cheapest = findAll(openDialog(ctx), 'pill').find(p => p.textContent === 'Cheapest');
  assert.ok(cheapest, 'the dialog offers no ranking by price');
  cheapest.onclick();
  assert.deepStrictEqual(findAll(openDialog(ctx), 'model-row').map(r => r.dataset.id), [SMALL.id, FABLE.id],
    'ranking by price did not put the cheaper model first');
});

test('closing the dialog keeps the model that was chosen', async () => {
  const ctx = newScreen({ '/models': [SMALL, FABLE], '/preferences': { model: SMALL.id } });
  const v = node('div');
  await ctx.viewNew(v);
  await find(v, 'model-choice').onclick();
  find(openDialog(ctx), 'dialog-close').onclick();
  assert.strictEqual(openDialog(ctx), null, 'the close button left the dialog open');
  assert.strictEqual((await startConversation(ctx, v)).model, SMALL.id);
});

// ---------- the environment dialog ----------

test('the granted environment is ticked in a dialog of names, never typed', async () => {
  const ctx = newScreen({
    '/sessions': [session({ granted_env: ['GH_TOKEN'] })],
    '/env': [
      { name: 'AGENT_REPO', from_env_file: true },
      { name: 'GH_TOKEN', from_env_file: true },
      { name: 'LANGUAGE', from_env_file: false },
    ],
  });
  const v = node('div');
  await ctx.viewNew(v);
  assert.strictEqual(findAll(v, 'text').filter(n => n.type === 'text').length, 0,
    'the screen still has a field to type variable names into');

  const open = find(v, 'env-choice');
  assert.ok(open, 'the granted environment is not a button that opens a dialog');
  await open.onclick();
  const dialog = openDialog(ctx);
  assert.ok(dialog, 'the button opened no dialog');

  const rows = findAll(dialog, 'env-row');
  const box = name => rows.find(r => r.dataset.name === name).children.find(c => c.type === 'checkbox');
  assert.deepStrictEqual(rows.map(r => r.dataset.name), ['AGENT_REPO', 'GH_TOKEN', 'LANGUAGE']);
  assert.strictEqual(box('GH_TOKEN').checked, true, 'a name already granted is not ticked');
  assert.strictEqual(box('AGENT_REPO').checked, false);
  assert.ok(textOf(dialog).includes('.env'), 'the dialog does not say which names come from .env');

  box('AGENT_REPO').checked = true;
  box('AGENT_REPO').onchange();
  find(dialog, 'dialog-done').onclick();
  assert.strictEqual(openDialog(ctx), null, 'done left the dialog open');
  assert.ok(textOf(find(v, 'env-summary')).includes('AGENT_REPO'), 'the screen does not show the new grant');
  assert.deepStrictEqual((await startConversation(ctx, v)).granted_env, ['GH_TOKEN', 'AGENT_REPO']);
});

// A name can be granted and then leave the environment. It must still be on
// the list, or it could be carried into every new conversation unseen.
test('a grant whose variable is gone is still listed, and can be unticked', async () => {
  const ctx = newScreen({ '/sessions': [session({ granted_env: ['OLD_TOKEN'] })], '/env': [] });
  const v = node('div');
  await ctx.viewNew(v);
  await find(v, 'env-choice').onclick();
  const row = findAll(openDialog(ctx), 'env-row').find(r => r.dataset.name === 'OLD_TOKEN');
  assert.ok(row, 'a grant no longer in the environment is not listed');
  assert.ok(/not set/.test(textOf(openDialog(ctx))), 'the dialog does not say the variable is not set');
  const cb = row.children.find(c => c.type === 'checkbox');
  cb.checked = false;
  cb.onchange();
  assert.deepStrictEqual((await startConversation(ctx, v)).granted_env, []);
});

// ---------- skills that ship off ----------

const SKILLS = { skills: [{ name: 'changing-yourself', default_enabled: false }, { name: 'scheduling', default_enabled: true }] };
const pills = v => findAll(v, 'pill').filter(p => ['changing-yourself', 'scheduling'].includes(p.textContent));
const isOn = p => String(p.className).split(/\s+/).includes('on');

test('a skill that ships off starts unticked in a configuration that did not choose skills', async () => {
  const ctx = newScreen({ '/skills': SKILLS, '/sessions': [session({ enabled_skills: null })] });
  const v = node('div');
  await ctx.viewNew(v);
  const byName = Object.fromEntries(pills(v).map(p => [p.textContent, isOn(p)]));
  assert.deepStrictEqual(byName, { 'changing-yourself': false, scheduling: true });
  assert.strictEqual((await startConversation(ctx, v)).enabled_skills, null,
    'an untouched configuration should leave the skills to their defaults');
});

test('ticking a skill that ships off chooses the skills, and unticking it goes back to the defaults', async () => {
  const ctx = newScreen({ '/skills': SKILLS, '/sessions': [session({ enabled_skills: null })] });
  const v = node('div');
  await ctx.viewNew(v);
  pills(v).find(p => p.textContent === 'changing-yourself').onclick();
  assert.ok(isOn(pills(v).find(p => p.textContent === 'changing-yourself')), 'the pill did not turn on');
  pills(v).find(p => p.textContent === 'changing-yourself').onclick();
  pills(v).find(p => p.textContent === 'changing-yourself').onclick();
  const body = await startConversation(ctx, v);
  assert.deepStrictEqual(Array.from(body.enabled_skills).sort(), ['changing-yourself', 'scheduling']);
});

test('all turns on the skills that ship off as well', async () => {
  const ctx = newScreen({ '/skills': SKILLS });
  const v = node('div');
  await ctx.viewNew(v);
  const skillsBar = findAll(v, 'act').filter(b => b.textContent === 'all')[1];
  skillsBar.onclick();
  assert.ok(pills(v).every(isOn), 'all left a skill off');
  assert.deepStrictEqual(Array.from((await startConversation(ctx, v)).enabled_skills).sort(), ['changing-yourself', 'scheduling']);
});

// What a conversation may read is part of what it is, so the controls screen
// says it rather than leaving it to be discovered when a tool succeeds.
test('the controls screen names the variables a conversation may read', async () => {
  const ctx = load({
    '/sessions/S1': { session: session({ id: 'S1', granted_env: ['GH_TOKEN', 'AGENT_REPO'] }) },
    '/tools': { tools: [] }, '/skills': { skills: [] }, '/models': [],
  });
  // route() is what fills in which session the screen is for; the view is then
  // driven into a node of this test's own, so what it rendered can be read.
  ctx.location.hash = '#settings/S1';
  ctx.route();
  const v = node('div');
  await ctx.viewSettings(v);
  // The first note is the summary of what this conversation is; the editor
  // below it has prose of its own, which is not what is being asked about here.
  const summary = findAll(v, 'note')[0].textContent;
  assert.ok(summary.includes('GH_TOKEN') && summary.includes('AGENT_REPO'),
    'the summary does not say which variables the conversation may read: ' + summary);
});

// The opposite, so the line above is not simply always printed: a conversation
// granted nothing says nothing about grants.
test('a conversation granted nothing says nothing about grants', async () => {
  const ctx = load({
    '/sessions/S1': { session: session({ id: 'S1' }) },
    '/tools': { tools: [] }, '/skills': { skills: [] }, '/models': [],
  });
  ctx.location.hash = '#settings/S1';
  ctx.route();
  const v = node('div');
  await ctx.viewSettings(v);
  const summary = findAll(v, 'note')[0].textContent;
  assert.ok(!summary.includes('may read'),
    'a conversation with no grants still claims it may read something: ' + summary);
});
