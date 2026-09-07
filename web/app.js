'use strict';

const $ = id => document.getElementById(id);
const el = (tag, cls, text) => { const n = document.createElement(tag); if (cls) n.className = cls; if (text != null) n.textContent = text; return n; };
const api = async (path, opts) => {
  const r = await fetch(path, opts);
  if (r.status === 204) return null;
  const body = await r.text();
  let json = null; try { json = body ? JSON.parse(body) : null; } catch (e) {}
  if (!r.ok) throw new Error((json && json.error) || body || r.status);
  return json;
};
const post = (p, b) => api(p, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(b || {}) });
const patch = (p, b) => api(p, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify(b) });
const del = p => api(p, { method: 'DELETE' });

// Wherever one session names another, the identifier is the only route between
// them. Rendered as text it is 21 characters to copy by hand, so every occurrence
// becomes a link. Built from split parts, so nothing but text and anchors is ever
// inserted — the id came from the transcript, not from a template.
function sessionLinks(text) {
  const frag = document.createDocumentFragment();
  for (const p of splitSessionIds(text)) {
    if (!p.id) { frag.append(document.createTextNode(p.text)); continue; }
    const a = el('a', 'sid', p.text);
    a.href = '#session/' + p.id;
    a.title = 'open session ' + p.id;
    frag.append(a);
  }
  return frag;
}
const fmtMoney = v => '$' + (v || 0).toFixed(4);
const fmtBytes = n => !n ? '0 B' : n < 1024 ? n + ' B' : n < 1048576 ? (n / 1024).toFixed(1) + ' kB' : n < 1073741824 ? (n / 1048576).toFixed(1) + ' MB' : (n / 1073741824).toFixed(2) + ' GB';
const ago = t => {
  const d = (Date.now() - new Date(t)) / 1000;
  if (d < 60) return Math.max(0, Math.round(d)) + 's ago';
  if (d < 3600) return Math.round(d / 60) + 'm ago';
  if (d < 86400) return Math.round(d / 3600) + 'h ago';
  return Math.round(d / 86400) + 'd ago';
};
const until = t => {
  const d = (new Date(t) - Date.now()) / 1000;
  if (d < 0) return 'due';
  if (d < 60) return 'in ' + Math.round(d) + 's';
  if (d < 3600) return 'in ' + Math.round(d / 60) + 'm';
  if (d < 86400) return 'in ' + Math.round(d / 3600) + 'h';
  return 'in ' + Math.round(d / 86400) + 'd';
};

const state = { view: null, session: null, entries: [], streaming: '', sessions: [], models: [], status: null, collapsed: {}, sort: 'recent' };

// ---------- routing ----------

function route() {
  const h = location.hash.slice(1) || 'sessions';
  const [name, arg] = h.split('/');
  state.view = name;
  state.arg = arg;
  drawer(false);
  closeMenu();
  render();
  renderSidebar();
}
window.addEventListener('hashchange', route);

// ---------- shell ----------

// The rail is a column of the grid on a laptop and an overlay on a phone. Only
// the phone can open and close it, and one class on the body says which it is.
function drawer(open) { document.body.className = open ? 'drawer' : ''; }

$('menu').onclick = () => drawer(true);
$('side-close').onclick = () => drawer(false);
$('scrim').onclick = () => drawer(false);
$('side-new').onclick = () => location.hash = '#new';
$('side-search').onclick = () => location.hash = '#search';
$('side-all').onclick = () => location.hash = '#sessions';
$('back').onclick = () => history.length > 1 ? history.back() : (location.hash = '#sessions');

// A view's own actions are one list rendered twice: along the header where
// there is room for them, and behind a single button where there is not. CSS
// decides which of the two is on screen, so neither can disagree with the other.
const PANELS = [['jobs', 'Jobs'], ['memory', 'Memory'], ['tools', 'Tools'], ['skills', 'Skills'], ['panels', 'More']];

let folded = null;
function closeMenu() {
  if (!folded) return;
  folded.remove();
  folded = null;
}

function toggleMenu(wrap, actions) {
  if (folded) return closeMenu();
  folded = el('div', 'menu');
  for (const a of actions) {
    const b = el('button', a.danger ? 'danger' : '', a.label);
    b.onclick = () => { closeMenu(); a.fn(); };
    folded.append(b);
  }
  wrap.append(folded);
}
document.addEventListener('click', closeMenu);

// ---------- websocket ----------

let ws;
function connect() {
  ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws');
  ws.onmessage = ev => handle(JSON.parse(ev.data));
  ws.onclose = () => setTimeout(connect, 2000);
}
function handle(e) {
  if (e.kind === 'entry' || e.kind === 'transient') {
    if (e.kind === 'entry') announce(e.entry, e.session_id);
    if (state.view === 'session' && e.session_id === state.arg) {
      if (e.kind === 'entry') state.entries.push(e.entry);
      state.streaming = '';
      renderTranscript();
    }
  } else if (e.kind === 'delta') {
    if (state.view === 'session' && e.session_id === state.arg) {
      state.streaming += e.text;
      renderStreaming();
    }
  } else if (e.kind === 'turn_start') {
    state.streaming = '';
  } else if (e.kind === 'sessions' || e.kind === 'jobs' || e.kind === 'status') {
    if (['sessions', 'jobs', 'panels'].includes(state.view)) render();
    // The rail is on screen whatever the view is, so it follows every change to
    // the list rather than only the one the session list happens to be showing.
    if (e.kind !== 'jobs') renderSidebar();
    if (e.kind === 'status') loadStatus();
  }
}

// announce shows a system notification for a message the operator is not
// looking at. It is best-effort by design: without permission the unread count
// in the session list is the whole signal, and nothing opens inside the app.
function announce(entry, sessionID) {
  if (!shouldNotify(entry, sessionID, { view: state.view, arg: state.arg, visible: !document.hidden })) return;
  if (!canShowSystemNotification({ hasNotification: 'Notification' in window, permission: 'Notification' in window ? Notification.permission : '' })) return;
  const known = (state.sessions || []).find(x => x.id === sessionID);
  const n = messageNotification(entry, sessionID, known && known.title);
  try {
    const sys = new Notification(n.title, { body: n.body, tag: n.tag });
    sys.onclick = () => { window.focus(); location.hash = '#session/' + n.sessionID; sys.close(); };
  } catch (err) {}
}

// toast is in-app feedback for what the operator just did: an upload that
// failed, a reload that worked. Nothing the agent does arrives this way.
function toast(n) {
  if (!n) return;
  const t = el('div', 'toast');
  t.append(el('b', null, n.title || ''), el('span', null, n.body || ''));
  t.onclick = () => { t.remove(); if (n.session_id) location.hash = '#session/' + n.session_id; };
  document.body.append(t);
  setTimeout(() => t.remove(), 8000);
}

function setHeader(title, showBack, actions) {
  $('title').textContent = title;
  $('back').hidden = !showBack;
  const acts = $('head-acts');
  acts.innerHTML = '';
  const wrap = $('more-wrap');
  const list = actions || [];
  wrap.hidden = !list.length;
  for (const a of list) {
    const b = el('button', a.danger ? 'danger' : '', a.label);
    b.onclick = a.fn;
    acts.append(b);
  }
  $('more').onclick = e => {
    e.stopPropagation();
    toggleMenu(wrap, list);
  };
}

// ---------- rail ----------

// The rail carries the way to everything: a conversation to switch to, a panel
// to open, and what the gateway costs. It is redrawn on every route change and
// whenever the agent says a session or the status moved.
async function renderSidebar() {
  const nav = $('side-nav');
  nav.innerHTML = '';
  for (const [hash, label] of PANELS) {
    const b = el('button', state.view === hash ? 'on' : '', label);
    b.onclick = () => location.hash = '#' + hash;
    nav.append(b);
  }
  renderSideFoot();
  const list = $('side-list');
  let sessions = state.sessions;
  try { sessions = await api('/sessions'); state.sessions = sessions; } catch (e) {}
  list.innerHTML = '';
  const ordered = sortSessions((sessions || []).filter(s => s.status === 'active'), 'recent')
    .concat(sortSessions((sessions || []).filter(s => s.status !== 'active'), 'recent'));
  if (!ordered.length) { list.append(el('div', 's', 'No conversations yet.')); return; }
  for (const s of ordered) list.append(sideItem(s));
}

// A rail row answers one question — which conversation is this — so it carries
// the title, when it last moved, and what is waiting in it. Everything else
// about a session lives on its row in the list view.
function sideItem(s) {
  const on = state.view === 'session' && state.arg === s.id;
  const b = el('button', 'side-item' + (on ? ' on' : '') + (s.status === 'active' ? '' : ' arch'));
  const m = el('div', 'm');
  m.append(el('div', 'n', s.title || 'Untitled'));
  const bits = [ago(s.last_active_at)];
  if (s.job_count) bits.push(s.job_count + ' job' + (s.job_count > 1 ? 's' : ''));
  if (s.status !== 'active') bits.push('archived');
  m.append(el('div', 's', bits.join(' · ')));
  b.append(m);
  if (s.unread) b.append(el('span', 'badge', String(s.unread)));
  b.onclick = () => location.hash = '#session/' + s.id;
  return b;
}

function renderSideFoot() {
  const f = $('side-foot');
  f.innerHTML = '';
  const line = (label, val) => {
    const l = el('div', 'l');
    l.append(document.createTextNode(label));
    l.append(el('b', null, val));
    f.append(l);
  };
  const st = state.status;
  const k = st && st.key;
  if (k) line('credit ', k.remaining != null ? fmtMoney(k.remaining) + ' left' : fmtMoney(k.usage) + ' used');
  const sb = st && st.sandbox;
  if (sb) line('sandbox ', sb.mechanism && sb.mechanism !== 'none' ? sb.mechanism : 'not enforced');
  if (!k && !sb) line('gateway ', st ? 'reachable' : 'unreachable');
}

// ---------- views ----------

// A transcript is read in a column; a list of jobs, tools, or files is read
// across the room a laptop actually has.
const ROOMY = ['sessions', 'jobs', 'tools', 'skills', 'files', 'panels', 'memory', 'search', 'toolpanel'];

function render() {
  $('foot').innerHTML = '';
  closeMenu();
  const v = $('view');
  v.className = 'wide' + (ROOMY.includes(state.view || 'sessions') ? ' roomy' : '');
  v.innerHTML = '';
  switch (state.view) {
    case 'session': return viewSession(v);
    case 'panels': return viewPanels(v);
    case 'jobs': return viewJobs(v);
    case 'memory': return viewMemory(v);
    case 'tools': return viewTools(v);
    case 'skills': return viewSkills(v);
    case 'search': return viewSearch(v);
    case 'settings': return viewSettings(v);
    case 'fork': return viewFork(v);
    case 'files': return viewFiles(v);
    case 'new': return viewNew(v);
    case 'toolpanel': return viewToolPanel(v);
    default: return viewSessions(v);
  }
}

// ---------- sessions list ----------

// Recency is the default because the list is usually read to get back to what
// you were doing. Size is the other question worth asking of it: rotation copies
// a session's working directory into its successor, so disk use accumulates down
// a chain and the biggest holder is never the one you were last in.
function sortSessions(list, by) {
  const out = (list || []).slice();
  if (by === 'size') return out.sort((a, b) => (b.disk_bytes || 0) - (a.disk_bytes || 0));
  return out.sort((a, b) => new Date(b.last_active_at) - new Date(a.last_active_at));
}

async function viewSessions(v) {
  setHeader('Conversations', false);
  const bar = el('div', 'toolbar');
  const nw = el('button', 'btn primary only-narrow', 'New conversation');
  nw.onclick = () => location.hash = '#new';
  const search = el('button', 'btn only-narrow', 'Search');
  search.onclick = () => location.hash = '#search';
  const imp = el('button', 'btn', 'Import');
  const impFile = el('input'); impFile.type = 'file'; impFile.accept = '.zip'; impFile.hidden = true;
  imp.onclick = () => impFile.click();
  impFile.onchange = async () => {
    if (!impFile.files.length) return;
    const fd = new FormData();
    fd.append('file', impFile.files[0]);
    try {
      const s = await api('/sessions/import', { method: 'POST', body: fd });
      location.hash = '#session/' + s.id;
    } catch (e) { toast({ title: 'Import failed', body: String(e.message) }); }
    impFile.value = '';
  };
  const order = el('button', 'btn', 'Sort: ' + (state.sort === 'size' ? 'size' : 'recent'));
  order.title = 'order by last activity or by disk used';
  order.onclick = () => { state.sort = state.sort === 'size' ? 'recent' : 'size'; render(); };
  bar.append(nw, search, imp, order, impFile);
  v.append(bar);

  const list = await api('/sessions');
  state.sessions = list;
  const active = list.filter(s => s.status === 'active');
  const archived = list.filter(s => s.status !== 'active');
  const section = (label, items) => {
    if (!items.length) return;
    v.append(el('h2', null, label));
    sortSessions(items, state.sort).forEach(s => v.append(sessionRow(s)));
  };
  section('active', active);
  section('archived', archived);
  if (!list.length) v.append(el('div', 'empty', 'No conversations yet. Start one to give the agent something to hold.'));
}

function sessionRow(s) {
  const row = el('button', 'row-item');
  const m = el('div', 'm');
  m.append(el('div', 'n', s.title || 'Untitled'));
  // Two lines, because one long grey run of six facts is read as none of them.
  // What is scanned — when it last moved, what it holds, what it cost — comes
  // first; what the session is configured as comes second.
  const sub = el('div', 's');
  sub.textContent = `${ago(s.last_active_at)} · ${fmtBytes(s.disk_bytes)} · ${fmtMoney(s.cost)}`;
  const cfg = el('div', 's');
  cfg.textContent = `${s.model.split('/').pop()} · ${s.entry_count} entries · ${s.context_used.toLocaleString()}/${s.rotate_at_tokens.toLocaleString()} tok`;
  m.append(sub, cfg);
  if (s.job_count) { const t = el('span', 'tag on', s.job_count + ' job' + (s.job_count > 1 ? 's' : '')); m.append(t); }
  if (s.continued_by) m.append(el('span', 'tag', '→ continued'));
  row.append(m);
  if (s.unread) row.append(el('span', 'badge', String(s.unread)));
  // Deleting is why the size is shown, so it is offered on the same row rather
  // than inside the session it would remove.
  const rm = el('span', 'tag danger', 'delete');
  rm.onclick = async e => {
    e.stopPropagation();
    if (!confirm('Delete "' + (s.title || 'Untitled') + '", its transcript, its files, and its jobs?')) return;
    await del('/sessions/' + s.id);
    render();
  };
  row.append(rm);
  row.onclick = () => location.hash = '#session/' + s.id;
  return row;
}

// ---------- conversation ----------

async function viewSession(v) {
  const res = await api('/sessions/' + state.arg);
  if (res.redirected_to) {
    toast({ title: 'Archived conversation', body: 'Opened the live session of this chain instead.' });
    location.hash = '#session/' + res.redirected_to;
    return;
  }
  state.session = res.session;
  state.entries = await api('/sessions/' + state.arg + '/transcript');
  const id = state.session.id;
  // What you do to a conversation belongs in its header, not on top of its
  // first message: the transcript starts at the top of the screen.
  setHeader(state.session.title || 'Conversation', true, [
    { label: 'Jobs', fn: () => location.hash = '#jobs/' + id },
    { label: 'Files', fn: () => location.hash = '#files/' + id },
    { label: 'Summary', fn: showSummary },
    { label: 'Fork', fn: () => location.hash = '#fork/' + id },
    { label: 'Controls', fn: () => location.hash = '#settings/' + id },
  ]);
  await post('/sessions/' + state.arg + '/read', {});

  const t = el('div'); t.id = 'transcript';
  v.append(t);
  renderTranscript();

  const foot = $('foot');
  const status = el('div', 'status'); status.id = 'statusline';
  const form = el('form', 'composer');
  const ta = el('textarea'); ta.rows = 1; ta.placeholder = 'Message the agent';
  ta.oninput = () => { ta.style.height = 'auto'; ta.style.height = Math.min(ta.scrollHeight, 180) + 'px'; };
  // A keyboard sends on Enter, because that is what a keyboard expects. A touch
  // screen has no comfortable shift, so there Enter is a newline and the button
  // is how a message is sent.
  ta.onkeydown = e => {
    if (!sendsOnEnter(e, hasKeyboard())) return;
    e.preventDefault();
    form.requestSubmit();
  };
  const up = el('button', 'attach', '＋'); up.type = 'button'; up.title = 'Attach a file';
  const file = el('input'); file.type = 'file'; file.hidden = true;
  up.onclick = () => file.click();
  file.onchange = async () => {
    if (!file.files.length) return;
    const fd = new FormData();
    fd.append('file', file.files[0]);
    try { const r = await api('/sessions/' + state.session.id + '/files', { method: 'POST', body: fd }); toast({ title: 'Uploaded', body: r.path }); }
    catch (e) { toast({ title: 'Upload failed', body: String(e.message) }); }
    file.value = '';
  };
  const send = el('button', 'send', '↑'); send.type = 'submit'; send.title = 'Send'; send.setAttribute && send.setAttribute('aria-label', 'Send');
  form.append(up, file, ta, send);
  form.onsubmit = async e => {
    e.preventDefault();
    const text = ta.value.trim();
    if (!text) return;
    ta.value = ''; ta.style.height = 'auto';
    await post('/sessions/' + state.session.id + '/messages', { text });
  };
  const hint = el('div', 'hint', 'Enter sends · Shift+Enter for a new line');
  foot.append(status, form, hint);
  renderStatus();
  scrollDown();
}

// sendsOnEnter decides what Enter means in the composer. On a keyboard it sends,
// because that is what a keyboard expects, and shift is the newline. A touch
// screen has no comfortable shift, so there Enter is a newline and the button is
// the only send. Either way the platform's own combination still sends.
function sendsOnEnter(e, keyboard) {
  if (e.key !== 'Enter' || e.isComposing) return false;
  if (e.metaKey || e.ctrlKey) return true;
  return !!keyboard && !e.shiftKey && !e.altKey;
}

// hasKeyboard is asked before Enter is given a meaning. It answers false where
// the query is unavailable, so an unknown device keeps the safer of the two.
function hasKeyboard() {
  try { return !!(window.matchMedia && window.matchMedia('(pointer: fine)').matches); }
  catch (e) { return false; }
}

function renderStatus() {
  const s = state.session;
  const line = $('statusline');
  if (!line || !s) return;
  line.innerHTML = '';
  const pct = Math.min(100, 100 * s.context_used / s.rotate_at_tokens);
  // The bar changes colour where rotation stops being far off, because that is
  // the point at which the number is worth reading.
  const bar = el('span', 'bar' + (pct >= 80 ? ' hot' : '')); const fill = el('i'); fill.style.width = pct + '%'; bar.append(fill);
  const add = (label, val) => { const w = el('span'); if (label) w.append(document.createTextNode(label + ' ')); w.append(el('b', null, val)); line.append(w); };
  add('', s.model.split('/').pop());
  line.append(bar);
  add('', `${s.context_used.toLocaleString()} / ${s.rotate_at_tokens.toLocaleString()} tok`);
  add('cost', fmtMoney(s.cost));
  if (s.cache_hit_rate) add('cached', Math.round(s.cache_hit_rate * 100) + '%');
  const last = [...state.entries].reverse().find(e => e.usage && e.usage.tokens_per_second);
  if (last) add('', last.usage.tokens_per_second.toFixed(1) + ' tok/s');
  if (state.streaming) line.append(el('span', 'gen', 'generating'));
}

async function loadStatus() {
  if (!state.session) return;
  const res = await api('/sessions/' + state.session.id);
  state.session = res.session;
  renderStatus();
}

function scrollDown() {
  const m = $('main');
  requestAnimationFrame(() => m.scrollTop = m.scrollHeight);
}

function renderTranscript() {
  const t = $('transcript');
  if (!t) return;
  const atBottom = $('main').scrollHeight - $('main').scrollTop - $('main').clientHeight < 120;
  t.innerHTML = '';
  for (const e of state.entries) t.append(renderEntry(e));
  if (state.streaming) t.append(streamNode());
  if (atBottom) scrollDown();
  renderStatus();
}

function renderStreaming() {
  const t = $('transcript');
  if (!t) return;
  let n = document.getElementById('streaming');
  if (!n) { n = streamNode(); t.append(n); }
  n.querySelector('.bub').innerHTML = renderMarkdown(state.streaming);
  scrollDown();
}

function streamNode() {
  const w = el('div', 'msg'); w.id = 'streaming';
  w.append(el('div', 'who', 'agent'), bubble({ role: 'assistant', text: state.streaming }));
  return w;
}

// The agent writes markdown, so the agent's turn is rendered as markdown — the
// only model output in the interface that becomes markup, and markdown.js
// escapes it before it does. What the operator typed stays exactly as typed:
// rendering it would rewrite their own words back at them, and a job's wake is
// the stored prompt, not prose.
function bubble(e) {
  const b = el('div', 'bub');
  if (e.role === 'assistant') { b.className = 'bub md'; b.innerHTML = renderMarkdown(e.text); }
  else b.textContent = e.text;
  return b;
}

function renderEntry(e) {
  if (e.type === 'prompt') return promptEntry(e);
  if (e.type === 'event') {
    // Laid out as a log line — clock, kind, detail — so it cannot be read as
    // something the agent said or a tool returned.
    const n = el('div', 'ev' + (isFailure(e) ? ' bad' : ''));
    const detail = el('span', 'detail');
    detail.append(sessionLinks(eventDetail(e)));
    n.append(el('span', 'at', eventTime(e.created_at)),
             el('span', 'kind', eventLabel(e)),
             detail);
    n.title = new Date(e.created_at).toLocaleString();
    return n;
  }
  if (e.role === 'tool') {
    const res = toolResult(e.tool_result);
    const failed = res.ok === false;
    const body = resultBody(res);
    const n = el('div', 'tool' + (failed ? ' err' : ''));
    const head = el('div', 't', '← ' + e.tool_name);
    head.style.cursor = 'pointer';
    const hint = resultPreview(body);
    const peek = hint ? el('span', 'peek', '  ·  ' + hint) : null;
    const pre = el('pre', null, body);
    // A failure is open by default: an error the operator has to go looking for
    // is an error they will not see.
    const show = open => { pre.hidden = !open; if (peek) peek.hidden = open; };
    show(failed);
    head.onclick = () => show(pre.hidden);
    if (peek) head.append(peek);
    n.append(head, pre);
    return n;
  }
  // Carried messages are marked by the style alone. Which session they came from
  // is said once, in the seeding event at the top, rather than on every bubble.
  const w = el('div', 'msg ' + bubbleClass(e) + (e.carried_from ? ' carried' : ''));
  const who = el('div', 'who', speaker(e));
  // The job's name is the only way back to the row that scheduled it.
  if (e.job_id && e.role === 'user') { who.style.cursor = 'pointer'; who.onclick = () => location.hash = '#jobs'; }
  who.append(el('span', 'at', messageTime(e.created_at)));
  w.append(who);
  if (e.text) w.append(bubble(e));
  for (const c of (e.tool_calls || [])) {
    // A call previews what it asked for exactly as its result previews what came
    // back: half an exchange is not readable on its own.
    const n = el('div', 'tool');
    const head = el('div', 't', '→ ' + c.name);
    head.style.cursor = 'pointer';
    const hint = callPreview(c.arguments);
    const peek = hint ? el('span', 'peek', '  ·  ' + hint) : null;
    const pre = el('pre', null, callBody(c.arguments));
    const show = open => { pre.hidden = !open; if (peek) peek.hidden = open; };
    show(false);
    head.onclick = () => show(pre.hidden);
    if (peek) head.append(peek);
    n.append(head, pre);
    w.append(n);
  }
  return w;
}

function promptEntry(e) {
  const box = el('div', 'prompt');
  const head = el('div', 'head');
  const total = (e.sections || []).reduce((a, s) => a + s.tokens, 0);
  head.append(el('span', null, 'prompt'), el('span', 'sp', ''), el('span', null, total.toLocaleString() + ' tokens'));
  box.append(head);
  for (const s of (e.sections || [])) {
    const sec = el('div', 'sec');
    const row = el('div', 'row');
    const label = s.name === 'memory'
      ? `memory · ${s.text === '(empty)' ? 0 : s.text.split('\n').length} items`
      : s.name.replace(/_/g, ' ');
    row.append(el('div', 'n', label), el('div', 'tok', s.tokens.toLocaleString()));
    const key = e.seq + ':' + s.name;
    const open = !!state.collapsed[key];
    const toggle = el('button', null, open ? 'collapse' : 'expand');
    toggle.onclick = () => { state.collapsed[key] = !open; renderTranscript(); };
    if (s.editable) {
      const ed = el('button', null, 'edit');
      ed.onclick = () => location.hash = '#memory';
      row.append(ed);
    }
    row.append(toggle);
    sec.append(row);
    if (open) { const pre = el('pre'); pre.append(sessionLinks(s.text)); sec.append(pre); }
    box.append(sec);
  }
  return box;
}

async function showSummary() {
  const s = state.session;
  const v = $('view');
  v.innerHTML = '';
  setHeader('Summary', true);
  $('foot').innerHTML = '';
  v.append(el('p', 'note', 'The rolling summary is what a rotation carries forward. Editing it changes what the next session is told.'));
  const ta = el('textarea'); ta.style.minHeight = '320px';
  ta.className = 'text'; ta.value = s.summary || '(no summary yet — this session has not grown past the threshold)';
  const save = el('button', 'btn primary', 'Save summary');
  save.onclick = async () => { await patch('/sessions/' + s.id, { summary: ta.value }); location.hash = '#session/' + s.id; };
  v.append(ta, save);
}

// ---------- session configuration ----------

const describeSet = (set, noun) => set == null ? 'all ' + noun : (set.length ? set.length + ' ' + noun : 'no ' + noun);

// configEditor renders a configuration and returns a reader for it. It does not
// save anything; what the caller does with it depends on whether the session has
// taken a turn, which is when model, tools, and skills stop being editable.
async function configEditor(v, current) {
  const cfg = {
    model: current.model || null,
    enabled_tools: current.enabled_tools == null ? null : current.enabled_tools.slice(),
    enabled_skills: current.enabled_skills == null ? null : current.enabled_skills.slice(),
  };

  v.append(el('h2', null, 'model'));
  const search = el('input', 'text');
  search.type = 'search';
  search.placeholder = 'search models \u2014 provider, family, or version';
  const sel = el('select', 'text');
  const note = el('p', 'note', '');
  v.append(search, sel, note);

  // The catalogue runs to hundreds of tool-calling models, so it is narrowed by
  // the search rather than scrolled. The chosen model stays in the list even
  // when the query excludes it, so searching can never silently change it.
  const fill = (models, q) => {
    sel.innerHTML = '';
    const keep = cfg.model && !models.some(m => m.id === cfg.model);
    const opts = (keep ? [{ id: cfg.model }] : []).concat(models);
    for (const m of (opts.length ? opts : [{ id: cfg.model || '' }])) {
      const o = el('option', null, `${m.id}${m.context_length ? '  \u00b7 ' + Math.round(m.context_length / 1000) + 'k' : ''}`);
      o.value = m.id;
      if (m.id === cfg.model) o.selected = true;
      sel.append(o);
    }
    if (!cfg.model) cfg.model = sel.value;
    if (models.length) note.textContent = `${models.length} model${models.length === 1 ? '' : 's'}` + (q ? ` match \u201c${q}\u201d` : ' support tool calling');
    else if (q) note.textContent = `No model matches \u201c${q}\u201d \u2014 keeping ${cfg.model}.`;
    else note.textContent = `Model list unavailable \u2014 keeping ${cfg.model || 'the default'}.`;
  };

  // Each keystroke supersedes the one before it, so a slow answer to an earlier
  // query must not overwrite the list a later one already drew.
  let seq = 0;
  const load = async q => {
    const mine = ++seq;
    const models = await api('/models' + (q ? '?q=' + encodeURIComponent(q) : '')).catch(() => []);
    if (mine !== seq) return;
    if (!q) state.models = models;
    fill(models, q);
  };
  let debounce = null;
  search.oninput = () => {
    clearTimeout(debounce);
    debounce = setTimeout(() => load(search.value.trim()), 150);
  };
  sel.onchange = () => { cfg.model = sel.value; };
  if (state.models.length) fill(state.models, ''); else await load('');

  const picker = async (label, path, key) => {
    const data = await api(path);
    const names = (data.tools || data.skills).map(x => x.name);
    v.append(el('h2', null, label));
    const wrap = el('div');
    const draw = () => {
      wrap.innerHTML = '';
      for (const n of names) {
        const on = cfg[key] === null || cfg[key].includes(n);
        const p = el('button', 'pill' + (on ? ' on' : ''), n);
        p.onclick = () => {
          const cur = cfg[key] === null ? names.slice() : cfg[key].slice();
          cfg[key] = on ? cur.filter(x => x !== n) : cur.concat(n);
          if (cfg[key].length === names.length) cfg[key] = null;
          draw();
        };
        wrap.append(p);
      }
    };
    const bar = el('div');
    const all = el('button', 'act', 'all');
    const none = el('button', 'act', 'none');
    all.onclick = () => { cfg[key] = null; draw(); };
    none.onclick = () => { cfg[key] = []; draw(); };
    bar.append(all, none);
    v.append(bar, wrap);
    draw();
  };
  await picker('tools', '/tools', 'enabled_tools');
  await picker('skills', '/skills', 'enabled_skills');

  return () => cfg;
}

async function viewNew(v) {
  setHeader('New conversation', true);
  const recent = state.sessions[0] || (await api('/sessions').catch(() => []) || [])[0] || {};
  const read = await configEditor(v, recent);
  const start = el('button', 'btn primary', 'Start conversation');
  start.onclick = async () => {
    const s = await post('/sessions', read());
    location.hash = '#session/' + s.id;
  };
  const done = el('div', 'finish'); done.append(start);
  v.append(done);
}

// ---------- files ----------

// A session's files are its working directory: what its tools see, what an
// upload lands in, and what an export carries.
async function viewFiles(v) {
  const id = state.arg;
  setHeader('Files', true);
  v.append(el('p', 'note',
    'This conversation has a working directory of its own. Its tools run there, uploads land there, ' +
    'and a fork starts from a copy of it.'));

  const bar = el('div');
  const up = el('button', 'btn primary', 'Upload file');
  const file = el('input'); file.type = 'file'; file.multiple = true; file.hidden = true;
  up.onclick = () => file.click();
  file.onchange = async () => {
    for (const f of file.files) {
      up.textContent = 'uploading ' + f.name + '…';
      const fd = new FormData();
      fd.append('file', f);
      try { await api('/sessions/' + id + '/files', { method: 'POST', body: fd }); }
      catch (e) { toast({ title: 'Upload failed', body: f.name + ': ' + e.message }); }
    }
    file.value = '';
    render();
  };
  const exp = el('button', 'btn', 'Export session');
  exp.onclick = () => location.href = '/sessions/' + id + '/export';
  bar.append(up, file, exp);
  v.append(bar);

  const files = await api('/sessions/' + id + '/files');
  const inFiles = files.reduce((n, f) => n + f.bytes, 0);
  const total = (await api('/sessions/' + id)).session.disk_bytes;
  v.append(el('div', 'status', `${fmtBytes(total)} on disk · ${files.length} file${files.length === 1 ? '' : 's'} of ${fmtBytes(inFiles)} · transcript ${fmtBytes(total - inFiles)}`));
  if (!files.length) { v.append(el('div', 'empty', 'No files yet.')); return; }
  for (const f of files) {
    const row = el('button', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', f.path), el('div', 's', `${fmtBytes(f.bytes)} · ${ago(f.modified_at)}`));
    row.append(m);
    const rm = el('span', 'tag', 'delete');
    rm.onclick = async e => {
      e.stopPropagation();
      if (!confirm('Delete ' + f.path + '?')) return;
      await del('/sessions/' + id + '/files/' + f.path.split('/').map(encodeURIComponent).join('/'));
      render();
    };
    row.append(rm);
    row.onclick = () => window.open('/sessions/' + id + '/files/' + f.path.split('/').map(encodeURIComponent).join('/'), '_blank');
    v.append(row);
  }
}

// Forking is the same act as starting a conversation: choose the configuration,
// then create the session. The predecessor's settings are the starting point.
async function viewFork(v) {
  const id = state.arg;
  const res = await api('/sessions/' + id);
  setHeader('Fork', true);
  v.append(el('p', 'note',
    'A fork continues this conversation in a new session, carrying its summary and recent turns ' +
    'across. Its configuration is chosen here and fixed once the fork exists.'));
  const read = await configEditor(v, res.session);
  const go = el('button', 'btn primary', 'Create fork');
  go.onclick = async () => {
    const succ = await post('/sessions/' + id + '/rotate', Object.assign({ archive: false }, read()));
    location.hash = '#session/' + succ.id;
  };
  const done = el('div', 'finish'); done.append(go);
  v.append(done);
}

async function viewSettings(v) {
  const id = state.arg;
  const res = await api('/sessions/' + id);
  const s = res.session;
  setHeader('Controls', true);

  const runs = `${s.model} with ${describeSet(s.enabled_tools, 'tools')} and ${describeSet(s.enabled_skills, 'skills')}`;
  v.append(el('p', 'note',
    `This conversation runs on ${runs}, fixed for its life. Changing any of it continues the ` +
    `conversation in a new session, carrying the summary and recent turns across.`));

  const read = await configEditor(v, s);
  const go = el('button', 'btn primary', 'Continue in a new session');
  go.onclick = async () => {
    const succ = await post('/sessions/' + id + '/rotate', Object.assign({ archive: true }, read()));
    location.hash = '#session/' + succ.id;
  };
  const done = el('div', 'finish'); done.append(go);
  v.append(done);

  v.append(el('h2', null, 'files'));
  const files = el('button', 'btn', 'Files and export');
  files.onclick = () => location.hash = '#files/' + id;
  v.append(files);

  v.append(el('h2', null, 'danger'));
  const d = el('button', 'btn danger', 'Delete conversation');
  d.onclick = async () => { if (confirm('Delete this conversation, its transcript, and its jobs?')) { await del('/sessions/' + id); location.hash = '#sessions'; } };
  v.append(d);
}

// ---------- panels ----------

async function viewToolPanel(v) {
  setHeader(state.arg, true);
  try {
    const mod = await import('/tools/' + state.arg + '/panel.js');
    await mod.default({ root: v, api, el });
  } catch (e) {
    v.append(el('div', 'empty', 'Panel failed to load: ' + e.message));
  }
}

async function viewPanels(v) {
  setHeader('Panels', true);
  const items = [
    ['jobs', 'Jobs', 'Schedules attached to conversations'],
    ['memory', 'Memory', 'What survives a conversation'],
    ['tools', 'Tools', 'Loaded tools and failures'],
    ['skills', 'Skills', 'What the agent knows how to do'],
    ['search', 'Search', 'Full text across every transcript'],
  ];
  const grid = el('div', 'cards');
  for (const [hash, name, sub] of items) {
    const row = el('button', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', name), el('div', 's', sub));
    row.append(m);
    row.onclick = () => location.hash = '#' + hash;
    grid.append(row);
  }
  v.append(grid);
  try {
    const data = await api('/tools');
    const withPanel = data.tools.filter(t => t.has_panel);
    if (withPanel.length) v.append(el('h2', null, 'tool panels'));
    const own = el('div', 'cards');
    for (const t of withPanel) {
      const row = el('button', 'row-item');
      const m = el('div', 'm');
      m.append(el('div', 'n', t.name), el('div', 's', t.description));
      row.append(m);
      row.onclick = () => location.hash = '#toolpanel/' + t.name;
      own.append(row);
    }
    if (withPanel.length) v.append(own);
  } catch (e) {}
  v.append(el('h2', null, 'device'), notifyControl());
  // A sandbox that quietly does nothing is worse than none, so this says which.
  const sb = state.status && state.status.sandbox;
  if (sb) {
    v.append(el('h2', null, 'isolation'));
    const on = sb.mechanism && sb.mechanism !== 'none';
    v.append(el('span', 'tag ' + (on ? 'on' : 'bad'), 'sandbox · ' + (sb.mechanism || 'none')));
    v.append(el('div', 's', on
      ? "A tool reaches this session's working directory and no other."
      : 'NOT ENFORCED: ' + (sb.reason || '') + ' — a tool can read any session\'s files and the agent\'s own.'));
  }
  if (state.status && state.status.key) {
    v.append(el('h2', null, 'openrouter'));
    const k = state.status.key;
    v.append(el('div', 's', `usage ${fmtMoney(k.usage)}${k.remaining != null ? ' · remaining ' + fmtMoney(k.remaining) : ''}`));
  }
}

async function viewJobs(v) {
  setHeader('Jobs', true);
  const scope = state.arg ? '&session_id=' + state.arg : '';
  const jobs = await api('/jobs' + (state.arg ? '?session_id=' + state.arg : ''));
  if (!jobs || !jobs.length) {
    v.append(el('div', 'empty', 'No jobs scheduled. The agent schedules its own from inside a conversation.'));
    return;
  }
  // A finished job is kept for its log, and a session that reminds every ten
  // minutes leaves one per reminder. Clearing them is offered only while there
  // are some, and says how many it will take.
  const finished = jobs.filter(j => j.status === 'done');
  if (finished.length) {
    const bar = el('div', 'toolbar');
    const clear = el('button', 'btn danger', `Delete ${finished.length} finished job${finished.length > 1 ? 's' : ''}`);
    clear.title = 'Deletes finished jobs and their run logs. Scheduled jobs are untouched.';
    clear.onclick = async () => {
      if (!confirm(`Delete ${finished.length} finished job${finished.length > 1 ? 's' : ''} and their run logs? Scheduled jobs are untouched.`)) return;
      try { await del('/jobs?status=done' + scope); }
      catch (e) { toast({ title: 'Nothing deleted', body: String(e.message) }); }
      render();
    };
    bar.append(clear);
    v.append(bar);
  }
  const grid = el('div', 'cards');
  v.append(grid);
  for (const j of jobs) {
    const row = el('div', 'row-item');
    const m = el('div', 'm');
    const name = el('div', 'n', j.prompt);
    name.title = j.prompt;
    m.append(name);
    m.append(el('span', 'tag ' + (j.status === 'done' ? '' : 'on'), j.status || 'scheduled'));
    // The job's own settings, as set. Nothing is derived and no category is
    // inferred — what you see is what the job is.
    const settings = el('dl', 'settings');
    const field = (k, val) => { settings.append(el('dt', null, k), el('dd', null, val)); };
    field('schedule', j.schedule);
    field('check', j.check || '—');
    field('after acting', j.after_acting);
    field('next wake', j.status === 'done' ? '—' : until(j.next_run_at));
    m.append(settings);
    m.append(el('div', 's', `${j.run_count} runs` + (j.last_status ? ` · last ${j.last_status.replace(/_/g, ' ')}` : '')));
    // Nothing refuses a cadence, so the price sits on the row that sets one.
    const price = jobCost(j.estimate);
    if (price && j.status !== 'done') m.append(el('div', 's cost', price));

    // The log is what makes a job checkable without reading the conversation it
    // fires into, so it opens in place rather than on a screen of its own.
    const log = el('div', 'joblog'); log.hidden = true;
    const toggle = el('button', 'btn', 'Log');
    let loaded = false;
    toggle.onclick = async () => {
      log.hidden = !log.hidden;
      if (log.hidden || loaded) return;
      loaded = true;
      log.textContent = 'loading…';
      try { renderRuns(log, await api('/jobs/' + j.id + '/runs')); }
      catch (e) { log.textContent = String(e.message); }
    };
    const acts = el('div');
    const open = el('button', 'btn', 'Open conversation');
    open.onclick = () => location.hash = '#session/' + j.session_id;
    const rm = el('button', 'btn danger', 'Delete');
    rm.onclick = async () => { await del('/jobs/' + j.id); render(); };
    acts.append(toggle, open, rm);
    m.append(acts, log);
    row.append(m);
    grid.append(row);
  }
}

// renderRuns lists what a job has done, newest first: when it woke, what came
// of it, and what was said.
function renderRuns(into, runs) {
  into.innerHTML = '';
  if (!runs || !runs.length) { into.append(el('div', 's', 'This job has not run yet.')); return; }
  for (const r of runs) {
    const line = el('div', 'run ' + (r.outcome === 'failed' ? 'bad' : ''));
    const head = el('div', 'h');
    head.append(el('span', 'at', runTime(r.at)), el('span', 'kind', r.outcome));
    if (r.due_at && lateBy(r)) head.append(el('span', 'late', lateBy(r)));
    line.append(head);
    if (r.message) line.append(el('div', 'b', r.message));
    into.append(line);
  }
}



async function viewMemory(v) {
  setHeader('Memory', true);
  const data = await api('/memory');
  v.append(el('div', 's', `${data.used} of ${data.capacity} characters used`));
  for (const m of (data.items || [])) {
    const row = el('div', 'row-item');
    const wrap = el('div', 'm');
    const inp = el('input', 'text'); inp.value = m.text;
    inp.onchange = async () => { await patch('/memory/' + m.id, { text: inp.value }); };
    wrap.append(inp);
    wrap.append(el('div', 's', `from ${m.source_session ? m.source_session.slice(-6) : 'the interface'} · ${ago(m.created_at)}`));
    const rm = el('button', 'btn danger', 'Delete');
    rm.onclick = async () => { await del('/memory/' + m.id); render(); };
    wrap.append(rm);
    row.append(wrap);
    v.append(row);
  }
  const add = el('input', 'text'); add.placeholder = 'Remember one fact…';
  add.onchange = async () => {
    try { await post('/memory', { text: add.value }); render(); }
    catch (e) { toast({ title: 'Memory is full', body: String(e.message) }); }
  };
  v.append(el('h2', null, 'add'), add);
}

async function viewTools(v) {
  setHeader('Tools', true);
  const data = await api('/tools');
  const rl = el('button', 'btn primary', 'Reload from disk');
  rl.onclick = async () => { const r = await post('/tools/reload', {}); toast({ title: 'Reloaded', body: (r.loaded || []).join(', ') }); render(); };
  const bar = el('div', 'toolbar'); bar.append(rl);
  v.append(bar);
  const grid = el('div', 'cards');
  for (const f of (data.failures || [])) {
    const row = el('div', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', f.dir), el('div', 's', f.reason));
    m.append(el('span', 'tag bad', 'not loaded'));
    row.append(m);
    grid.append(row);
  }
  for (const t of data.tools) {
    const row = el('div', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', t.name));
    m.append(el('div', 's', t.description));
    if (t.builtin) m.append(el('span', 'tag', 'builtin'));
    if (t.db_prefix) m.append(el('span', 'tag', t.db_prefix));
    if (t.has_panel) m.append(el('span', 'tag on', 'panel'));
    const pre = el('pre', null, JSON.stringify(t.parameters, null, 2));
    pre.hidden = true; pre.style.fontSize = '11px'; pre.style.whiteSpace = 'pre-wrap';
    const sh = el('button', 'btn', 'Manifest');
    sh.onclick = () => pre.hidden = !pre.hidden;
    m.append(sh, pre);
    row.append(m);
    grid.append(row);
  }
  v.append(grid);
}

async function viewSkills(v) {
  setHeader('Skills', true);
  const data = await api('/skills');
  const grid = el('div', 'cards');
  v.append(grid);
  for (const f of (data.failures || [])) {
    const row = el('div', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', f.dir), el('div', 's', f.reason), el('span', 'tag bad', 'skipped'));
    row.append(m); grid.append(row);
  }
  if (!data.skills.length && !(data.failures || []).length) v.append(el('div', 'empty', 'No skills on disk.'));
  for (const s of data.skills) {
    const row = el('button', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', s.name), el('div', 's', s.description), el('span', 'tag', s.bytes + ' bytes'));
    row.append(m);
    row.onclick = async () => {
      const full = await api('/skills/' + s.name);
      const v2 = $('view'); v2.innerHTML = '';
      setHeader(s.name, true);
      const pre = el('pre', null, full.body);
      pre.style.whiteSpace = 'pre-wrap'; pre.style.fontSize = '13px';
      v2.append(pre);
    };
    grid.append(row);
  }
}

async function viewSearch(v) {
  setHeader('Search', true);
  const inp = el('input', 'text'); inp.type = 'search';
  inp.placeholder = 'Search every transcript…';
  const out = el('div');
  inp.onchange = async () => {
    out.innerHTML = '';
    if (!inp.value.trim()) return;
    let hits = [];
    try { hits = await api('/search?q=' + encodeURIComponent(inp.value)) || []; }
    catch (e) { out.append(el('div', 'empty', String(e.message))); return; }
    if (!hits.length) { out.append(el('div', 'empty', 'No matches.')); return; }
    const grid = el('div', 'cards');
    out.append(grid);
    for (const h of hits) {
      const row = el('button', 'row-item');
      const m = el('div', 'm');
      m.append(el('div', 'n', h.title || h.session_id));
      m.append(el('div', 's', h.snippet));
      row.append(m);
      row.onclick = () => location.hash = '#session/' + h.session_id;
      grid.append(row);
    }
  };
  v.append(inp, out);
  inp.focus();
}

// ---------- notifications ----------

// notifyControl states what this browser will do with a new message, and asks
// for permission when it can. A device that will never show one has to say so:
// silence and "working, nothing to report" look identical otherwise.
function notifyControl() {
  const box = el('div');
  const has = 'Notification' in window;
  const perm = has ? Notification.permission : '';
  const line = (cls, text) => box.append(el('span', cls, text));
  if (!has) {
    line('tag bad', 'notifications · unavailable');
    box.append(el('div', 's', 'This browser has no notification API here. It needs a secure origin — https, or localhost. The unread count on the session list is the only signal.'));
    return box;
  }
  if (perm === 'denied') {
    line('tag bad', 'notifications · blocked');
    box.append(el('div', 's', 'Blocked for this site in the browser\'s own settings. Allow it there to turn banners back on.'));
    return box;
  }
  if (perm === 'granted') {
    line('tag on', 'notifications · on');
    box.append(el('div', 's', 'A new agent message shows a banner while this browser is open and you are not reading that conversation. Nothing reaches you once the browser is closed.'));
    return box;
  }
  line('tag warn', 'notifications · off');
  box.append(el('div', 's', 'Not enabled on this device. Until it is, a new message only changes the unread count.'));
  const b = el('button', 'btn primary', 'Enable notifications');
  b.onclick = async () => {
    try { await Notification.requestPermission(); } catch (e) {}
    render();
  };
  box.append(b);
  return box;
}

// ---------- boot ----------

async function boot() {
  connect();
  route();
  try {
    state.status = await api('/status');
    const b = state.status.breaker;
    $('breaker').textContent = b && b.open ? 'Scheduler paused: ' + b.reason : '';
  } catch (e) {
    $('breaker').textContent = 'Cannot reach the gateway. Are you on the tailnet?';
  }
  renderSideFoot();
}
boot();
