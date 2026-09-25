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
const put = (p, b) => api(p, { method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify(b) });
const patch = (p, b) => api(p, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify(b) });
const del = p => api(p, { method: 'DELETE' });

// The server refuses a file over this, so the interface refuses it first: on a
// phone, sending it all only to be told so costs minutes of data.
const MAX_UPLOAD = 250 << 20;

// uploadFile puts one file into a session's working directory, reporting how
// much of it has gone. It is the one request made by XMLHttpRequest, because
// fetch cannot say how far along a body it is sending has got. A refusal that
// trying again cannot change is marked final.
function uploadFile(sessionId, f, onProgress) {
  return new Promise((resolve, reject) => {
    if (f.size > MAX_UPLOAD) {
      const e = new Error('over 250 MB, which is refused'); e.final = true;
      return reject(e);
    }
    const x = new XMLHttpRequest();
    x.open('POST', '/sessions/' + sessionId + '/files');
    x.upload.onprogress = e => { if (e.lengthComputable && onProgress) onProgress(e.loaded / e.total); };
    x.onload = () => {
      let json = null; try { json = JSON.parse(x.responseText); } catch (e) {}
      if (x.status >= 200 && x.status < 300) return resolve(json);
      // A proxy in front of the agent answers in HTML of its own, which is no
      // use in a line of text; its status says as much.
      reject(new Error((json && json.error) ||
        (x.status === 413 ? 'too large for the server' : 'the server answered ' + x.status)));
    };
    x.onerror = () => reject(new Error('the connection dropped'));
    const fd = new FormData();
    fd.append('file', f);
    x.send(fd);
  });
}

// Wherever one session names another, the identifier is the only route between
// them. Rendered as text it is 21 characters to copy by hand, so every occurrence
// becomes a link. Built from split parts, so nothing but text and anchors is ever
// inserted — the id came from the transcript, not from a template.
function sessionLinks(text) {
  const frag = document.createDocumentFragment();
  for (const p of splitSessionIds(text)) {
    if (!p.id) { frag.append(document.createTextNode(p.text)); continue; }
    const a = el('a', 'sid', p.text);
    a.href = (p.kind === 'comparison' ? '#compared/' : '#session/') + p.id;
    a.title = p.kind === 'comparison' ? 'open comparison ' + p.id : 'open session ' + p.id;
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

// working holds, per session, when this page learned the agent started on it,
// in this browser's clock.
const state = { view: null, session: null, entries: [], streaming: '', fileFolds: {}, sessions: [], models: [], modelSort: 'intelligence', status: null, collapsed: {}, sort: 'recent', working: {} };

// ---------- routing ----------

function route() {
  const h = location.hash.slice(1) || 'sessions';
  const [name, arg] = h.split('/');
  // Where this screen was reached from, so a screen that finishes — an editor
  // that saved — can go back to it rather than push a second copy of it.
  state.from = state.here || '';
  state.here = '#' + h;
  state.view = name;
  state.arg = arg;
  drawer(false);
  closeMenu();
  closeDialog();
  const drawn = render();
  renderSidebar();
  return drawn;
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
// Personas are chosen when a conversation starts and written rarely, so they are
// reached from More rather than from the rail.
const PANELS = [['jobs', 'Jobs'], ['tools', 'Tools'], ['skills', 'Skills'], ['panels', 'More']];

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
    const col = comparing(e.session_id);
    if (col) {
      if (e.kind === 'entry') col.entries.push(e.entry);
      col.streaming = '';
      renderCompare();
    }
  } else if (e.kind === 'delta') {
    if (state.view === 'session' && e.session_id === state.arg) {
      state.streaming += e.text;
      renderStreaming();
    }
    const col = comparing(e.session_id);
    if (col) {
      col.streaming += e.text;
      renderCandidateStream(col);
    }
  } else if (e.kind === 'turn_start') {
    state.streaming = '';
    const col = comparing(e.session_id);
    if (col) col.streaming = '';
  } else if (e.kind === 'working' || e.kind === 'idle') {
    // A wait already counting — started when the message was sent — keeps its
    // start; the server's word only confirms it.
    if (e.kind === 'idle') delete state.working[e.session_id];
    else if (state.working[e.session_id] == null) state.working[e.session_id] = Date.now();
    // The transcript carries the thinking row, so it follows the change, and a
    // turn that has ended goes to its answer: that is what was waited for.
    if (state.view === 'session' && e.session_id === state.arg) {
      renderTranscript();
      if (e.kind === 'idle') scrollDown();
    }
    const col = comparing(e.session_id);
    if (col) {
      if (e.kind === 'idle') col.toEnd = true;
      renderCompare();
    }
  } else if (e.kind === 'skills' || e.kind === 'personas') {
    // Written from another window, or by a second browser. The screen that
    // lists them follows; every other screen reads them when it next opens.
    if (state.view === e.kind) render();
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
const ROOMY = ['sessions', 'jobs', 'tools', 'skills', 'personas', 'files', 'panels', 'search', 'toolpanel', 'compared'];

// A screen is drawn from requests that answer in their own time, so two screens
// asked for in quick succession can answer in the wrong order. Each draw keeps
// the number it started as, and a draw that is no longer the newest stops where
// it stands rather than painting over the screen that replaced it.
let drawing = 0;
const stale = gen => gen !== drawing;

function render() {
  drawing++;
  $('foot').innerHTML = '';
  closeMenu();
  const v = $('view');
  v.className = 'wide' + (ROOMY.includes(state.view || 'sessions') ? ' roomy' : '');
  v.innerHTML = '';
  switch (state.view) {
    case 'session': return viewSession(v);
    case 'panels': return viewPanels(v);
    case 'jobs': return viewJobs(v);
    case 'tools': return viewTools(v);
    case 'skills': return viewSkills(v);
    case 'personas': return viewPersonas(v);
    case 'search': return viewSearch(v);
    case 'settings': return viewSettings(v);
    case 'fork': return viewFork(v);
    case 'compare': return viewCompare(v);
    case 'compared': return viewCompared(v);
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
  const gen = drawing;
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
  if (stale(gen)) return;
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
  cfg.textContent = `${s.model.split('/').pop()} · ${s.entry_count} entries · ${s.context_used.toLocaleString()}/${s.compact_at_tokens.toLocaleString()} tok`;
  m.append(sub, cfg);
  if (s.job_count) { const t = el('span', 'tag on', s.job_count + ' job' + (s.job_count > 1 ? 's' : '')); m.append(t); }
  if (s.comparison) {
    // The mark is also the way back: a comparison screen left behind is found
    // again through the candidates it left in the list.
    const t = el('span', 'tag on', 'comparing');
    t.onclick = e => { e.stopPropagation(); location.hash = '#compared/' + s.comparison; };
    m.append(t);
  }
  else if (s.forked_from) m.append(el('span', 'tag', 'fork'));
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
  const gen = drawing;
  const res = await api('/sessions/' + state.arg);
  if (stale(gen)) return;
  if (res.redirected_to) {
    toast({ title: 'Archived conversation', body: 'Opened the live session of this chain instead.' });
    location.hash = '#session/' + res.redirected_to;
    return;
  }
  const entries = await api('/sessions/' + state.arg + '/transcript');
  if (stale(gen)) return;
  state.session = res.session;
  state.entries = entries;
  const id = state.session.id;
  // Opened mid-turn, the wait counts from when the agent started, not from now.
  if (res.session.working_seconds != null) state.working[id] = Date.now() - res.session.working_seconds * 1000;
  else delete state.working[id];
  // What you do to a conversation belongs in its header, not on top of its
  // first message: the transcript starts at the top of the screen.
  setHeader(state.session.title || 'Conversation', true, [
    // First, and only while the comparison is undecided: it is the screen this
    // conversation was opened from, and the one it is judged on.
    ...(state.session.comparison
      ? [{ label: 'Comparison', fn: () => location.hash = '#compared/' + state.session.comparison }]
      : []),
    { label: 'Jobs', fn: () => location.hash = '#jobs/' + id },
    { label: 'Files', fn: () => location.hash = '#files/' + id },
    { label: 'Copy', fn: () => copyConversation() },
    { label: 'Compact', fn: () => compactNow(id) },
    { label: 'Fork', fn: () => location.hash = '#fork/' + id },
    { label: 'Compare', fn: () => location.hash = '#compare/' + id },
    { label: 'Details', fn: () => location.hash = '#settings/' + id },
  ]);
  await post('/sessions/' + state.arg + '/read', {});
  if (stale(gen)) return;

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
  // A file uploads the moment it is picked and waits above the composer as a
  // chip until the message goes. An upload writes nothing into the conversation,
  // so the message names what it carries, and the agent learns of it there.
  const sessionId = state.session.id;
  const attached = [];
  const chips = el('div', 'chips');
  const drawChips = () => {
    chips.innerHTML = '';
    chips.hidden = !attached.length;
    for (const a of attached) {
      const c = el('button', 'chip' + (a.error ? ' failed' : a.path ? '' : ' going')); c.type = 'button';
      const how = a.error ? 'failed: ' + a.error + (a.final ? '' : ' · tap to retry')
        : a.path ? fmtBytes(a.size) : Math.round(a.progress * 100) + '%';
      c.append(el('span', 'name', a.name), el('span', 's', how));
      const x = el('span', 'x', '×'); x.title = 'Take off this message';
      x.onclick = e => { e.stopPropagation(); attached.splice(attached.indexOf(a), 1); drawChips(); };
      c.append(x);
      c.onclick = a.error && !a.final ? () => startUpload(a) : null;
      chips.append(c);
    }
  };
  const startUpload = a => {
    a.error = null; a.progress = 0;
    drawChips();
    uploadFile(sessionId, a.file, p => { a.progress = p; drawChips(); })
      .then(r => { a.path = r.path; drawChips(); })
      .catch(e => { a.error = e.message; a.final = !!e.final; drawChips(); });
  };
  drawChips();
  const up = el('button', 'attach', '＋'); up.type = 'button'; up.title = 'Attach a file';
  const file = el('input'); file.type = 'file'; file.multiple = true; file.hidden = true;
  up.onclick = () => file.click();
  file.onchange = () => {
    for (const f of Array.from(file.files || [])) {
      const a = { name: f.name, size: f.size, file: f, progress: 0 };
      attached.push(a);
      startUpload(a);
    }
    file.value = '';
  };
  const send = el('button', 'send', '↑'); send.type = 'submit'; send.title = 'Send'; send.setAttribute && send.setAttribute('aria-label', 'Send');
  form.append(up, file, ta, send);
  form.onsubmit = async e => {
    e.preventDefault();
    // A message that named a file still on its way, or one that never arrived,
    // would send the agent looking for something that is not there.
    const pending = attached.find(a => !a.path);
    if (pending) {
      toast(pending.error
        ? { title: 'Not sent', body: pending.name + ' did not upload. Retry it or take it off.' }
        : { title: 'Still uploading', body: 'Send once ' + pending.name + ' has arrived.' });
      return;
    }
    // Plain names: a message you wrote is shown as written, not as markdown.
    const names = attached.map(a => a.path).join(', ');
    const text = [ta.value.trim(), names && 'Attached: ' + names].filter(Boolean).join('\n\n');
    if (!text) return;
    attached.length = 0; drawChips();
    ta.value = ''; ta.style.height = 'auto';
    // The wait starts when the message leaves, not when the server first says
    // so: that gap is part of what the operator is waiting through.
    const sid = state.session.id;
    if (state.working[sid] == null) state.working[sid] = Date.now();
    renderTranscript();
    scrollDown();
    try { await post('/sessions/' + sid + '/messages', { text }); }
    catch (err) {
      delete state.working[sid];
      renderTranscript();
      toast({ title: 'Not sent', body: String(err.message) });
    }
  };
  const hint = el('div', 'hint', 'Enter sends · Shift+Enter for a new line');
  foot.append(status, chips, form, hint);
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
  const pct = Math.min(100, 100 * s.context_used / s.compact_at_tokens);
  // The bar changes colour where rotation stops being far off, because that is
  // the point at which the number is worth reading.
  const bar = el('span', 'bar' + (pct >= 80 ? ' hot' : '')); const fill = el('i'); fill.style.width = pct + '%'; bar.append(fill);
  const add = (label, val) => { const w = el('span'); if (label) w.append(document.createTextNode(label + ' ')); w.append(el('b', null, val)); line.append(w); };
  // First on the line, because while it is there it is the only thing on the
  // line worth reading.
  const since = state.working[s.id];
  if (since != null) {
    line.append(el('span', 'gen working', 'working ' + waitingLabel((Date.now() - since) / 1000)));
    // A turn runs for as long as its work takes, so while one is running the
    // way to end it belongs next to the count of how long it has been.
    const stop = el('button', 'stop', 'stop');
    stop.type = 'button'; stop.title = 'Stop this turn';
    stop.onclick = async e => {
      e && e.stopPropagation && e.stopPropagation();
      stop.disabled = true;
      try { await post('/sessions/' + s.id + '/cancel'); }
      catch (err) { toast({ title: 'Nothing to stop', body: String(err.message) }); }
    };
    line.append(stop);
    tickWorking();
  }
  add('', s.model.split('/').pop());
  line.append(bar);
  add('', `${s.context_used.toLocaleString()} / ${s.compact_at_tokens.toLocaleString()} tok`);
  add('cost', fmtMoney(s.cost));
  if (s.cache_hit_rate) add('cached', Math.round(s.cache_hit_rate * 100) + '%');
  const last = [...state.entries].reverse().find(e => e.usage && e.usage.tokens_per_second);
  if (last) add('', last.usage.tokens_per_second.toFixed(1) + ' tok/s');
  if (state.streaming && since == null) line.append(el('span', 'gen', 'generating'));
}

// tickWorking redraws the status line once a second while the conversation on
// screen is waiting on the agent, and stops by itself when it is not.
let workingTicker = null;
function tickWorking() {
  if (workingTicker) return;
  workingTicker = setInterval(() => {
    const s = state.session;
    if (state.view !== 'session' || !s || state.working[s.id] == null) {
      clearInterval(workingTicker);
      workingTicker = null;
      return;
    }
    renderStatus();
  }, 1000);
}

// copyConversation puts the whole conversation on the clipboard as Markdown.
// The clipboard API needs a secure page — https or localhost — so a plain http
// one falls back to the selection the browser has always been able to copy.
async function copyConversation() {
  const s = state.session;
  const text = conversationText(state.entries, s && s.title);
  const messages = state.entries.filter(e => e.type === 'message' && e.role !== 'tool').length;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) await navigator.clipboard.writeText(text);
    else copyBySelection(text);
  } catch (err) {
    try { copyBySelection(text); }
    catch (e) { toast({ title: 'Not copied', body: String(err.message || err) }); return; }
  }
  toast({ title: 'Copied the conversation', body: `${messages} message${messages === 1 ? '' : 's'} · ${fmtBytes(text.length)} of Markdown` });
}

function copyBySelection(text) {
  const ta = el('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.append(ta);
  ta.select();
  const ok = document.execCommand && document.execCommand('copy');
  ta.remove();
  if (!ok) throw new Error('the browser would not copy');
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
  const covers = coveredThrough(state.entries);
  for (const e of state.entries) {
    const isFolded = covers > 0 && e.seq <= covers;
    if (isFolded && !state.revealCompacted) continue;
    const node = renderEntry(e);
    // Revealed, a folded entry is dimmed behind a gutter rule so it can never be
    // mistaken for something the agent can still see.
    if (isFolded) node.classList.add('folded');
    t.append(node);
  }
  if (state.streaming) t.append(streamNode());
  else if (state.session && state.working[state.session.id] != null) t.append(thinkingNode());
  if (atBottom) scrollDown();
  renderStatus();
}

function renderStreaming() {
  const t = $('transcript');
  if (!t) return;
  // The first word replaces the thinking row with the answer it was standing in for.
  if (t.querySelector('.thinking')) { renderTranscript(); scrollDown(); return; }
  let n = document.getElementById('streaming');
  if (!n) { n = streamNode(); t.append(n); }
  n.querySelector('.bub').innerHTML = renderMarkdown(state.streaming);
  scrollDown();
}

// thinkingNode stands where the agent's answer will appear, from the moment it
// is asked until its first word arrives, and between the steps of a turn.
function thinkingNode() {
  const w = el('div', 'msg thinking');
  w.append(el('div', 'who', 'agent'), el('div', 'bub', 'thinking'));
  return w;
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

// The transcript shows what the model sees. Entries a compaction covers are not
// rendered by default: showing messages the agent can no longer read is exactly
// the surprise the design forbids — you would read turn 12 and assume it knows.
// They are still there, one click away, and still on disk.
function coveredThrough(entries) {
  let covers = 0;
  for (const e of entries) if (e.type === 'compaction') covers = e.covers_through || 0;
  return covers;
}

function compactionEntry(e) {
  const n = el('div', 'compaction');
  const before = (e.tokens_before || 0).toLocaleString();
  const after = (e.tokens_after || 0).toLocaleString();
  n.append(el('div', 't',
    `⊙ COMPACTED · ${e.folded_turns || 0} turns · ${before} → ${after} tokens · cache reset`));
  n.append(el('div', 'note', 'The agent no longer sees the messages above this point. This is what it sees instead:'));

  const body = el('div', 'bub md'); body.innerHTML = renderMarkdown(e.text || '');
  n.append(body);

  const bar = el('div', 'row');
  const edit = el('button', 'btn', 'Edit');
  edit.onclick = () => {
    const ta = el('textarea'); ta.className = 'text'; ta.style.minHeight = '260px'; ta.value = e.text || '';
    const save = el('button', 'btn primary', 'Save');
    save.onclick = async () => {
      // An edit appends a superseding compaction rather than rewriting one: the
      // transcript is append-only, and what the summariser wrote stays beside it.
      await put('/sessions/' + state.session.id + '/compaction', { text: ta.value });
      location.hash = '#session/' + state.session.id;
    };
    body.replaceWith(ta); bar.replaceWith(save);
  };
  const reveal = el('button', 'btn', `Show ${e.folded_turns || 0} hidden turns`);
  reveal.onclick = () => { state.revealCompacted = !state.revealCompacted; renderTranscript(); };
  if (state.revealCompacted) reveal.textContent = 'Hide folded turns';
  bar.append(edit, reveal);
  n.append(bar);
  return n;
}

function renderEntry(e) {
  if (e.type === 'compaction') return compactionEntry(e);
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
    // What the tool wrote to standard error, behind a toggle of its own. It has
    // always been kept with the result and never shown, which made a tool that
    // half-worked unreadable from here. It stays closed even when the call
    // failed: the error is the answer, the logs are the evidence, and an
    // operator reading the conversation is not debugging it until they are.
    const logs = String(e.stderr || '').trim();
    if (logs) {
      const out = el('pre', 'logs', logs);
      out.hidden = true;
      const toggle = el('button', 'logtoggle', 'logs');
      toggle.onclick = () => {
        out.hidden = !out.hidden;
        toggle.className = 'logtoggle' + (out.hidden ? '' : ' on');
      };
      n.append(toggle, out);
    }
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
    row.append(el('div', 'n', s.name.replace(/_/g, ' ')), el('div', 'tok', s.tokens.toLocaleString()));
    const key = e.seq + ':' + s.name;
    const open = !!state.collapsed[key];
    const toggle = el('button', null, open ? 'collapse' : 'expand');
    toggle.onclick = () => { state.collapsed[key] = !open; renderTranscript(); };
    row.append(toggle);
    sec.append(row);
    if (open) { const pre = el('pre'); pre.append(sessionLinks(s.text)); sec.append(pre); }
    box.append(sec);
  }
  return box;
}

// Compacting on request folds the conversation now rather than at the threshold.
// The summary it writes is editable where it lands, in the transcript, rather
// than on a screen of its own.
async function compactNow(id) {
  if (!confirm('Fold the older turns of this conversation into a summary? ' +
               'Nothing is deleted — what is folded stays on disk and stays searchable.')) return;
  await post('/sessions/' + id + '/compact', {});
  location.hash = '#session/' + id;
}

// ---------- session configuration ----------

// What a set nobody chose means depends on the set — every tool, but only the
// skills that are on by default — so the caller says it.
const describeSet = (set, noun, unchosen) => set == null ? unchosen : (set.length ? set.length + ' ' + noun : 'no ' + noun);

// ---------- dialogs ----------

// A dialog is a choice that needs more room than the screen it is made on: the
// model catalogue, the environment. It slides over the page — up from the
// bottom on a phone, in from the right where there is room — and there is only
// ever one.
let dialog = null;

function openDialog(title) {
  closeDialog();
  const back = el('div', 'dialog-back');
  const box = el('div', 'dialog');
  box.setAttribute('role', 'dialog');
  box.setAttribute('aria-modal', 'true');
  box.setAttribute('aria-label', title);
  const head = el('div', 'dialog-head');
  const close = el('button', 'icon dialog-close', '×');
  close.title = 'Close';
  close.setAttribute('aria-label', 'Close');
  close.onclick = () => closeDialog();
  head.append(el('div', 'dialog-title', title), close);
  const body = el('div', 'dialog-body');
  const foot = el('div', 'dialog-foot');
  box.append(head, body, foot);
  back.append(box);
  back.onclick = e => { if (e && e.target === back) closeDialog(); };
  document.body.append(back);
  dialog = { back, body, foot };
  // Drawn closed first and opened a frame later, so the slide has somewhere to
  // start from.
  requestAnimationFrame(() => requestAnimationFrame(() => {
    if (dialog && dialog.back === back) back.className = 'dialog-back open';
  }));
  return dialog;
}

function closeDialog() {
  if (!dialog) return;
  const back = dialog.back;
  dialog = null;
  back.className = 'dialog-back';
  // Left in place long enough to slide back out.
  setTimeout(() => back.remove(), 300);
}
document.addEventListener('keydown', e => { if (e.key === 'Escape') closeDialog(); });

function factList(m) {
  const w = el('div', 'facts');
  for (const [label, value] of modelFacts(m)) {
    const f = el('span', 'fact');
    f.append(el('span', null, label + ' '), el('b', null, value));
    w.append(f);
  }
  return w;
}

const MODEL_SORTS = [['intelligence', 'Smartest'], ['price', 'Cheapest'], ['context', 'Largest context'], ['newest', 'Newest']];

// A row is a div acting as a button rather than a button, because it holds a
// link, and a link inside a button is not a link.
function modelRow(m, chosen, pick) {
  const row = el('div', 'model-row' + (chosen ? ' on' : ''));
  row.dataset.id = m.id;
  row.tabIndex = 0;
  row.setAttribute('role', 'button');
  const top = el('div', 'top');
  top.append(el('div', 'n', modelName(m)));
  if (chosen) top.append(el('span', 'tag on', 'chosen'));
  const link = el('a', 'openrouter', 'OpenRouter ↗');
  link.href = openRouterURL(m.id);
  link.target = '_blank';
  link.rel = 'noopener';
  link.title = 'Providers, uptime, and the full description, on OpenRouter';
  link.onclick = e => { if (e) e.stopPropagation(); };
  top.append(link);
  row.append(top, el('div', 's ident', m.id));
  if (m.description) row.append(el('div', 'desc', m.description));
  row.append(factList(m));
  row.onclick = pick;
  row.onkeydown = e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick(); } };
  return row;
}

// chooseModel opens the catalogue. It runs to hundreds of tool-calling models,
// so it is searched and sorted rather than scrolled. The model already chosen
// stays listed whatever the search says: searching never changes the choice,
// and only picking a row does.
async function chooseModel(current, pick) {
  const d = openDialog('Choose a model');
  const search = el('input', 'text');
  search.type = 'search';
  search.placeholder = 'search — provider, family, or version';
  const sorts = el('div', 'sorts');
  const note = el('p', 'note', '');
  const list = el('div', 'model-list');
  d.body.append(search, sorts, note, list);

  let models = state.models || [];
  let q = '';
  const draw = () => {
    sorts.innerHTML = '';
    for (const [key, label] of MODEL_SORTS) {
      const b = el('button', 'pill' + (state.modelSort === key ? ' on' : ''), label);
      b.onclick = () => { state.modelSort = key; draw(); };
      sorts.append(b);
    }
    list.innerHTML = '';
    const shown = sortModels(models, state.modelSort);
    if (current && !shown.some(m => m.id === current)) {
      shown.unshift((state.models || []).find(m => m.id === current) || { id: current });
    }
    for (const m of shown) list.append(modelRow(m, m.id === current, () => { closeDialog(); pick(m.id); }));
    if (models.length) note.textContent = `${models.length} model${models.length === 1 ? '' : 's'}` + (q ? ` match “${q}”` : ' support tool calling');
    else if (q) note.textContent = `No model matches “${q}”.`;
    else note.textContent = 'The model list is unavailable.';
  };

  // Each keystroke supersedes the one before it, so a slow answer to an earlier
  // query must not overwrite the list a later one already drew.
  let seq = 0;
  const load = async query => {
    const mine = ++seq;
    const found = await api('/models' + (query ? '?q=' + encodeURIComponent(query) : '')).catch(() => []) || [];
    if (mine !== seq) return;
    if (!query) state.models = found;
    models = found;
    q = query;
    draw();
  };
  let debounce = null;
  search.oninput = () => {
    clearTimeout(debounce);
    debounce = setTimeout(() => load(search.value.trim()), 150);
  };
  if (models.length) draw(); else await load('');
  search.focus();
}

function envRow(name, on, change) {
  const row = el('label', 'env-row');
  row.dataset.name = name;
  const cb = el('input');
  cb.type = 'checkbox';
  cb.checked = on;
  cb.onchange = () => change(cb.checked);
  row.append(cb, el('span', null, name));
  return row;
}

// chooseGrants lists every name the agent could grant, ticked where this
// configuration grants it, and applies each tick as it is made. Names only: the
// values stay in the agent. A name already granted that the environment no
// longer holds is listed too, so it is never carried forward out of sight.
async function chooseGrants(current, change) {
  const d = openDialog('Granted environment');
  let chosen = current.slice();
  d.body.append(el('p', 'note',
    'Tick what this conversation’s tools may read. Only names are listed — values never leave ' +
    'the agent — and the model key is never offered. Leave everything unticked unless a skill asks for one.'));
  const filter = el('input', 'text');
  filter.type = 'search';
  filter.placeholder = 'filter names';
  const lists = el('div');
  d.body.append(filter, lists);

  const count = el('span', 's', '');
  const done = el('button', 'btn primary dialog-done', 'Done');
  done.onclick = () => closeDialog();
  d.foot.append(count, el('span', 'sp'), done);
  const tally = () => { count.textContent = chosen.length ? `${chosen.length} granted` : 'nothing granted'; };

  let offered = [];
  try { offered = await api('/env') || []; }
  catch (e) { lists.append(el('div', 'empty', 'The environment could not be listed: ' + e.message)); }
  const known = new Set(offered.map(x => x.name));
  const missing = chosen.filter(n => !known.has(n));
  const groups = [
    ['from .env', offered.filter(x => x.from_env_file).map(x => x.name)],
    ['environment', offered.filter(x => !x.from_env_file).map(x => x.name)],
    ['granted, not set', missing],
  ];
  const draw = () => {
    lists.innerHTML = '';
    const q = String(filter.value || '').trim().toUpperCase();
    for (const [label, names] of groups) {
      const shown = names.filter(n => !q || n.toUpperCase().includes(q));
      if (!shown.length) continue;
      lists.append(el('h2', null, label));
      for (const n of shown) {
        lists.append(envRow(n, chosen.includes(n), on => {
          chosen = on ? chosen.filter(x => x !== n).concat(n) : chosen.filter(x => x !== n);
          tally();
          change(chosen.slice());
        }));
      }
    }
    if (!offered.length && !missing.length) lists.append(el('div', 'empty', 'Nothing in the agent’s environment can be granted.'));
  };
  filter.oninput = draw;
  tally();
  draw();
}

// configEditor renders a configuration and returns a reader for it. It does not
// save anything; what the caller does with it depends on whether the session has
// taken a turn, which is when model, tools, and skills stop being editable.
async function configEditor(v, current) {
  const cfg = {
    model: current.model || null,
    enabled_tools: current.enabled_tools == null ? null : current.enabled_tools.slice(),
    enabled_skills: current.enabled_skills == null ? null : current.enabled_skills.slice(),
    granted_env: (current.granted_env || []).slice(),
    persona: current.persona || '',
  };

  // The model is one line on the screen and a dialog to change it: the facts
  // that choose between hundreds of models do not fit in a select.
  v.append(el('h2', null, 'model'));
  const choice = el('button', 'choice model-choice');
  const drawChoice = () => {
    choice.innerHTML = '';
    const m = (state.models || []).find(x => x.id === cfg.model) || { id: cfg.model || '' };
    const text = el('div', 'm');
    text.append(el('div', 'n', m.id ? modelName(m) : 'Choose a model'));
    if (m.name) text.append(el('div', 's ident', m.id));
    choice.append(text, el('span', 'go', 'Change'), factList(m));
  };
  choice.onclick = () => chooseModel(cfg.model, id => { cfg.model = id; drawChoice(); });
  v.append(choice);
  if (!state.models.length) state.models = await api('/models').catch(() => []) || [];
  drawChoice();

  // A set nobody chose is the defaults: every tool, and every skill but those
  // that ship off. It stays unchosen (null) for as long as it matches them, so
  // a skill added to disk later still reaches conversations that never chose.
  const picker = async (label, path, key) => {
    const data = await api(path);
    const items = data.tools || data.skills;
    const names = items.map(x => x.name);
    const defaults = items.filter(x => x.default_enabled !== false).map(x => x.name);
    const settle = set => set.length === defaults.length && set.every(x => defaults.includes(x)) ? null : set;
    v.append(el('h2', null, label));
    const wrap = el('div');
    const draw = () => {
      wrap.innerHTML = '';
      for (const n of names) {
        const on = cfg[key] === null ? defaults.includes(n) : cfg[key].includes(n);
        const p = el('button', 'pill' + (on ? ' on' : ''), n);
        if (!defaults.includes(n)) p.title = 'Off unless a conversation chooses it';
        p.onclick = () => {
          const cur = cfg[key] === null ? defaults.slice() : cfg[key].slice();
          cfg[key] = settle(on ? cur.filter(x => x !== n) : cur.concat(n));
          draw();
        };
        wrap.append(p);
      }
    };
    const bar = el('div');
    const all = el('button', 'act', 'all');
    const none = el('button', 'act', 'none');
    all.onclick = () => { cfg[key] = settle(names.slice()); draw(); };
    none.onclick = () => { cfg[key] = settle([]); draw(); };
    bar.append(all, none);
    v.append(bar, wrap);
    draw();
  };
  await picker('tools', '/tools', 'enabled_tools');
  await picker('skills', '/skills', 'enabled_skills');

  // The persona is the opening section of the prompt, and choosing one replaces
  // the built-in whole. That is said here rather than discovered later, because
  // a persona that leaves out the working rules is a different agent and nothing
  // downstream says why.
  const personas = ((await api('/personas').catch(() => null)) || {}).personas || [];
  v.append(el('h2', null, 'persona'));
  v.append(el('p', 'note', 'Who the agent is. Choosing one replaces the built-in persona entirely, ' +
    'working rules included. Write and edit them under More → Personas.'));
  const pwrap = el('div');
  const drawPersonas = () => {
    pwrap.innerHTML = '';
    for (const p of personas) {
      const on = (cfg.persona || 'default') === p.name;
      const b = el('button', 'pill' + (on ? ' on' : ''), p.name);
      b.title = p.description;
      b.onclick = () => { cfg.persona = p.name === 'default' ? '' : p.name; drawPersonas(); };
      pwrap.append(b);
    }
  };
  v.append(pwrap);
  drawPersonas();

  // Names, not values. The agent already holds the values; what a conversation
  // is given is permission to see one, and a name is safe to show, export, and
  // read back. The names are ticked from what the agent could grant, rather than
  // typed from memory and found misspelled when a tool fails.
  v.append(el('h2', null, 'granted environment'));
  v.append(el('p', 'note',
    'Variables this conversation\u2019s tools may read \u2014 a credential is granted here, not required ' +
    'by a tool. Leave it empty unless a skill asks for one.'));
  const envLine = el('div', 'env-line');
  const summary = el('div', 'env-summary');
  const drawGrants = () => {
    summary.innerHTML = '';
    if (!cfg.granted_env.length) summary.append(el('span', 's', 'Nothing granted.'));
    for (const n of cfg.granted_env) summary.append(el('span', 'tag', n));
  };
  const envOpen = el('button', 'btn env-choice', 'Choose variables');
  envOpen.onclick = () => chooseGrants(cfg.granted_env, next => { cfg.granted_env = next; drawGrants(); });
  envLine.append(summary, envOpen);
  v.append(envLine);
  drawGrants();

  return () => cfg;
}

// configFacts renders a configuration that cannot be changed. A conversation's
// is fixed for its life, so the screen that describes one states it: the same
// five headings the editor uses, so the two read alike, with nothing to press.
// A different configuration is a different conversation, reached from Fork.
async function configFacts(v, s) {
  v.append(el('h2', null, 'model'));
  const m = (state.models || []).find(x => x.id === s.model) || { id: s.model || '' };
  const line = el('div', 'fact fixed');
  const text = el('div', 'm');
  text.append(el('div', 'n', m.name ? modelName(m) : (m.id || 'none')));
  if (m.name) text.append(el('div', 's ident', m.id));
  line.append(text, factList(m));
  v.append(line);

  // A set nobody chose is the defaults, and the defaults live on disk rather
  // than on the session, so what the conversation actually runs with is only
  // known once they are fetched.
  const shown = async (label, path, chosen) => {
    const data = await api(path).catch(() => null) || {};
    const items = data.tools || data.skills || [];
    const names = chosen == null
      ? items.filter(x => x.default_enabled !== false).map(x => x.name)
      : chosen;
    v.append(el('h2', null, label));
    if (!names.length) { v.append(el('div', 's', 'None.')); return; }
    const wrap = el('div');
    for (const n of names) wrap.append(el('span', 'pill fixed', n));
    v.append(wrap);
  };
  await shown('tools', '/tools', s.enabled_tools);
  await shown('skills', '/skills', s.enabled_skills);

  v.append(el('h2', null, 'persona'));
  v.append(el('span', 'pill fixed', s.persona || 'default'));

  v.append(el('h2', null, 'granted environment'));
  const grants = s.granted_env || [];
  if (!grants.length) v.append(el('div', 's', 'Nothing granted.'));
  else {
    const wrap = el('div');
    for (const n of grants) wrap.append(el('span', 'pill fixed', n));
    v.append(wrap);
  }
}

async function viewNew(v) {
  const gen = drawing;
  setHeader('New conversation', true);
  const recent = state.sessions[0] || (await api('/sessions').catch(() => []) || [])[0] || {};
  // Tools, skills, and grants come from the most recent conversation. The model
  // is the one last chosen, which the most recent conversation need not be on.
  const prefs = await api('/preferences').catch(() => null) || {};
  if (stale(gen)) return;
  const read = await configEditor(v, Object.assign({}, recent, { model: prefs.model || recent.model }));
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
  const gen = drawing;
  const id = state.arg;
  setHeader('Files', true);
  v.append(el('p', 'note',
    'This conversation has a working directory of its own. Its tools run there, uploads land there, ' +
    'and a fork starts from a copy of it. Compacting does not touch it.'));

  const bar = el('div');
  const up = el('button', 'btn primary', 'Upload file');
  const file = el('input'); file.type = 'file'; file.multiple = true; file.hidden = true;
  up.onclick = () => file.click();
  file.onchange = async () => {
    for (const f of Array.from(file.files || [])) {
      up.textContent = 'uploading ' + f.name + '…';
      try {
        await uploadFile(id, f, p => { up.textContent = 'uploading ' + f.name + ' · ' + Math.round(p * 100) + '%'; });
      } catch (e) { toast({ title: 'Upload failed', body: f.name + ': ' + e.message }); }
    }
    file.value = '';
    render();
  };
  const exp = el('button', 'btn', 'Export session');
  exp.onclick = () => location.href = '/sessions/' + id + '/export';
  bar.append(up, file, exp);
  v.append(bar);

  const entries = await api('/sessions/' + id + '/files');
  const files = entries.filter(f => !f.dir);
  const inFiles = files.reduce((n, f) => n + f.bytes, 0);
  const total = (await api('/sessions/' + id)).session.disk_bytes;
  if (stale(gen)) return;
  v.append(el('div', 'status', `${fmtBytes(total)} on disk · ${files.length} file${files.length === 1 ? '' : 's'} of ${fmtBytes(inFiles)} · transcript ${fmtBytes(total - inFiles)}`));
  if (!entries.length) { v.append(el('div', 'empty', 'No files yet.')); return; }

  // Which directories are folded outlives a redraw — an upload, a delete — so
  // it is kept per session. A large tree starts folded and a small one open;
  // what the operator toggles is kept as the difference from that.
  const fold = state.fileFolds[id] || (state.fileFolds[id] = { folded: entries.length > OPEN_TREE_MAX, flipped: new Set() });
  const isFolded = p => fold.folded !== fold.flipped.has(p);
  const tree = el('div', 'tree');
  const fileURL = p => '/sessions/' + id + '/files/' + p.split('/').map(encodeURIComponent).join('/');
  const draw = () => {
    tree.innerHTML = '';
    const walk = (dir, depth) => {
      for (const n of dir.children) {
        const row = el('button', 'row-item' + (n.dir ? ' dir' : ''));
        row.dataset.depth = String(depth);
        row.style.paddingLeft = `calc(4px + ${depth} * 1.25rem)`;
        row.title = n.path;
        const m = el('div', 'm');
        if (n.dir) {
          const folded = isFolded(n.path);
          row.append(el('span', 'twisty', folded ? '▸' : '▾'));
          m.append(el('div', 'n', n.name + '/'), el('div', 's', !n.children.length ? 'empty'
            : `${n.children.length} item${n.children.length === 1 ? '' : 's'} · ${fmtBytes(n.bytes)}`));
          row.append(m);
          row.onclick = () => {
            if (fold.flipped.has(n.path)) fold.flipped.delete(n.path); else fold.flipped.add(n.path);
            draw();
          };
          tree.append(row);
          if (!folded) walk(n, depth + 1);
          continue;
        }
        m.append(el('div', 'n', n.name), el('div', 's', `${fmtBytes(n.bytes)} · ${ago(n.modified_at)}`));
        row.append(m);
        const rm = el('span', 'tag', 'delete');
        rm.onclick = async e => {
          e.stopPropagation();
          if (!confirm('Delete ' + n.path + '?')) return;
          await del(fileURL(n.path));
          render();
        };
        row.append(rm);
        row.onclick = () => window.open(fileURL(n.path), '_blank');
        tree.append(row);
      }
    };
    walk(fileTree(entries), 0);
  };
  draw();
  v.append(tree);
}

// A tree with more entries than this opens with its directories folded, so a
// cloned repository does not bury everything beside it.
const OPEN_TREE_MAX = 100;

// fileTree turns the flat listing into nested directories. Within each one,
// directories come first by name, then files newest first, as the flat list
// had them. A directory's size is everything beneath it.
function fileTree(entries) {
  const root = { path: '', dir: true, children: [] };
  const dirs = new Map([['', root]]);
  const dirOf = p => {
    if (dirs.has(p)) return dirs.get(p);
    const i = p.lastIndexOf('/');
    const d = { path: p, name: p.slice(i + 1), dir: true, bytes: 0, children: [] };
    dirs.set(p, d);
    dirOf(i < 0 ? '' : p.slice(0, i)).children.push(d);
    return d;
  };
  for (const f of entries) {
    if (f.dir) { dirOf(f.path); continue; }
    const i = f.path.lastIndexOf('/');
    dirOf(i < 0 ? '' : f.path.slice(0, i)).children.push(Object.assign({ name: f.path.slice(i + 1) }, f));
  }
  const settle = d => {
    d.children.sort((a, b) => a.dir !== b.dir ? (a.dir ? -1 : 1)
      : a.dir ? a.name.localeCompare(b.name) : String(b.modified_at).localeCompare(String(a.modified_at)));
    d.bytes = 0;
    for (const c of d.children) d.bytes += c.dir ? settle(c) : c.bytes;
    return d.bytes;
  };
  settle(root);
  return root;
}

// Forking is the same act as starting a conversation: choose the configuration,
// then create the session. The origin's settings are the starting point.
async function viewFork(v) {
  const gen = drawing;
  const id = state.arg;
  const res = await api('/sessions/' + id);
  if (stale(gen)) return;
  setHeader('Fork', true);
  v.append(el('p', 'note',
    'A fork copies this whole conversation into a new session. This one stays where it is, stays ' +
    'active, and keeps its jobs. The fork\'s configuration is chosen here and fixed once it exists.'));
  const read = await configEditor(v, res.session);
  const go = el('button', 'btn primary', 'Create fork');
  go.onclick = async () => {
    const succ = await post('/sessions/' + id + '/fork', read());
    location.hash = '#session/' + succ.id;
  };
  const done = el('div', 'finish'); done.append(go);
  v.append(done);
}

// ---------- comparing models ----------

// A comparison is several conversations at once, so what arrives over the socket
// is routed by the session it names rather than by the one screen being read.
function comparing(sessionID) {
  if (state.view !== 'compared' || !state.compare) return null;
  return state.compare.by[sessionID] || null;
}

// modelLabel is what a model is called where there is no room for its
// identifier: its catalogue name if the catalogue is loaded, else the part of
// the identifier that differs between the models being compared.
function modelLabel(id) {
  const m = (state.models || []).find(x => x.id === id);
  return m && m.name ? modelName(m) : String(id || '').split('/').pop();
}

// compareSetup holds the screen a comparison is composed on: the models chosen
// so far, and the nodes that say what that costs. The textarea outlives every
// redraw, so what has been typed is never lost to a change of models.
let compareSetup = null;

// Comparing is forking several times over. The screen says so, because the
// consequences are a fork's: the conversation it is started from does not move,
// keeps its jobs, and is never sent the message.
async function viewCompare(v) {
  const gen = drawing;
  const res = await api('/sessions/' + state.arg);
  if (stale(gen)) return;
  if (!(state.models || []).length) state.models = await api('/models').catch(() => []) || [];
  if (stale(gen)) return;
  const s = res.session;
  setHeader('Compare', true);
  v.append(el('p', 'note',
    'One message, put to several models at once. Each model answers in a fork of this conversation: ' +
    'it carries the whole transcript and a copy of the files, and runs a turn of its own. This ' +
    'conversation is not sent the message, stays where it is, and keeps its jobs. Keeping the answer ' +
    'you like best deletes the other candidates.'));

  v.append(el('h2', null, 'models'));
  const chosen = el('div', 'picked');
  const add = el('button', 'btn', 'Add a model');
  add.onclick = () => chooseModel(null, id => {
    if (!compareSetup.models.includes(id)) compareSetup.models.push(id);
    renderCompareSetup();
  });
  v.append(chosen, add);

  v.append(el('h2', null, 'message'));
  const ta = el('textarea', 'text');
  ta.rows = 4;
  ta.placeholder = 'What to ask all of them';
  ta.oninput = () => renderCompareSetup();
  v.append(ta);

  const price = el('p', 'note', '');
  const go = el('button', 'btn primary', 'Ask');
  const done = el('div', 'finish');
  done.append(go);
  v.append(price, done);

  compareSetup = { session: s, models: [s.model], chosen, ta, price, go };
  go.onclick = async () => {
    // Every press forks the conversation once per model and re-sends the whole
    // transcript, so a second press is not a retry: it is a second comparison,
    // paid for twice, answering a question already asked.
    if (go.disabled) return;
    go.disabled = true;
    go.textContent = 'Asking…';
    let out;
    try {
      out = await post('/sessions/' + s.id + '/compare',
        { text: compareSetup.ta.value.trim(), models: compareSetup.models.slice() });
    } catch (e) {
      toast({ title: 'Not asked', body: String(e.message) });
      renderCompareSetup();
      return;
    }
    location.hash = '#compared/' + out.comparison;
  };
  renderCompareSetup();
}

// The price is the one fact this screen has that the operator does not. A
// candidate is a fork onto another model, so it reads none of this
// conversation's cached prefix: every one of them re-sends the whole thing.
function renderCompareSetup() {
  if (!compareSetup) return;
  const { session: s, models, chosen, ta, price, go } = compareSetup;
  chosen.innerHTML = '';
  for (const m of models) {
    const p = el('button', 'pill on', modelLabel(m) + ' ×');
    p.title = m;
    p.onclick = () => {
      compareSetup.models = compareSetup.models.filter(x => x !== m);
      renderCompareSetup();
    };
    chosen.append(p);
  }
  price.textContent = models.length < 2
    ? 'Choose at least two models. A comparison of one is a fork.'
    : `${models.length} models, each sent this conversation in full: about ` +
      `${((s.context_used || 0) * models.length).toLocaleString()} tokens in all, ` +
      'none of it read from cache.';
  go.textContent = 'Ask ' + models.length + ' model' + (models.length === 1 ? '' : 's');
  go.disabled = models.length < 2 || !ta.value.trim();
}

// The candidates side by side, each filling in as its model answers. A column
// starts at the message they were all sent: everything above it is the history
// all of them share.
async function viewCompared(v) {
  const gen = drawing;
  const group = state.arg;
  const all = await api('/sessions').catch(() => []) || [];
  if (stale(gen)) return;
  const cands = (all || []).filter(s => s.comparison === group);
  setHeader('Comparison', true);
  if (!cands.length) {
    state.compare = null;
    v.append(el('div', 'empty', 'This comparison has been decided. The conversation you kept is in ' +
      'the list; the other candidates were deleted.'));
    return;
  }
  v.append(el('p', 'note', 'One message, ' + cands.length + ' models, each in a conversation of its ' +
    'own. Keeping one carries on in it and deletes the rest.'));
  const grid = el('div', 'candidates');
  v.append(grid);

  const cols = [], by = {};
  for (const c of cands) {
    if (c.working_seconds != null) state.working[c.id] = Date.now() - c.working_seconds * 1000;
    const col = { session: c, entries: [], streaming: '', stream: null };
    cols.push(col);
    by[c.id] = col;
  }
  state.compare = { id: group, cols, by, grid };
  renderCompare();
  for (const col of cols) {
    const entries = await api('/sessions/' + col.session.id + '/transcript').catch(() => []) || [];
    if (stale(gen)) return;
    col.entries = entries;
    renderCompare();
  }
}

function renderCompare() {
  const c = state.compare;
  if (!c || !c.grid) return;
  // Each column scrolls on its own and is rebuilt on every redraw, so where it
  // was is carried across: a column the reader left at its end stays at its
  // end, one scrolled back up stays put, and one that has just finished goes to
  // its answer.
  for (const col of c.cols) {
    const b = col.body;
    if (!b) continue;
    col.scroll = b.scrollTop;
    col.atEnd = b.scrollHeight - b.scrollTop - b.clientHeight < 40;
  }
  c.grid.innerHTML = '';
  for (const col of c.cols) c.grid.append(candidateColumn(col));
  for (const col of c.cols) {
    const b = col.body;
    b.scrollTop = col.toEnd || col.atEnd ? b.scrollHeight : (col.scroll || 0);
    col.toEnd = false;
  }
}

// A column is patched rather than redrawn while its model is writing, so the
// other columns are not rebuilt on every token.
function renderCandidateStream(col) {
  if (!col.stream) return renderCompare();
  const b = col.body;
  const atEnd = b.scrollHeight - b.scrollTop - b.clientHeight < 40;
  col.stream.querySelector('.bub').innerHTML = renderMarkdown(col.streaming);
  if (atEnd) b.scrollTop = b.scrollHeight;
}

function candidateColumn(col) {
  const s = col.session;
  const box = el('div', 'candidate');
  const head = el('div', 'candidate-head');
  head.append(el('div', 'n', modelLabel(s.model)), el('div', 's ident', s.model),
    el('div', 's', state.working[s.id] ? 'working…' : 'answered'));
  const body = el('div', 'candidate-body');
  for (const e of sinceQuestion(col.entries)) body.append(renderEntry(e));
  col.stream = null;
  if (col.streaming) {
    col.stream = el('div', 'msg');
    col.stream.append(el('div', 'who', 'agent'), bubble({ role: 'assistant', text: col.streaming }));
    body.append(col.stream);
  } else if (state.working[s.id]) body.append(thinkingNode());
  col.body = body;
  const acts = el('div', 'candidate-acts');
  const open = el('button', 'btn', 'Open');
  open.onclick = () => location.hash = '#session/' + s.id;
  const keep = el('button', 'btn primary', 'Keep this one');
  keep.onclick = () => keepCandidate(s);
  acts.append(open, keep);
  box.append(head, body, acts);
  return box;
}

// Every candidate holds the same history and differs only in what it did with
// the last message, so that is where the column starts.
function sinceQuestion(entries) {
  let at = 0;
  for (let i = (entries || []).length - 1; i >= 0; i--) {
    const e = entries[i];
    if (e.type === 'message' && e.role === 'user' && !e.job_id) { at = i; break; }
  }
  return (entries || []).slice(at);
}

// Keeping is the destructive half of comparing, so it says what goes before it
// goes. What is kept needs no explanation: it is the conversation from here on.
async function keepCandidate(s) {
  const others = state.compare ? state.compare.cols.length - 1 : 0;
  if (!confirm('Keep the answer from ' + modelLabel(s.model) + ' and carry on in it?\n\n' +
    'This deletes the other ' + others + ' conversation' + (others === 1 ? '' : 's') +
    ' in this comparison, with their transcripts and files.')) return;
  await post('/sessions/' + s.id + '/keep', {});
  state.compare = null;
  location.hash = '#session/' + s.id;
}

async function viewSettings(v) {
  const gen = drawing;
  const id = state.arg;
  const res = await api('/sessions/' + id);
  if (stale(gen)) return;
  const s = res.session;
  setHeader('Details', true);

  const grants = (s.granted_env || []);
  const runs = `${s.model} with ${describeSet(s.enabled_tools, 'tools', 'all tools')} and ${describeSet(s.enabled_skills, 'skills', 'the default skills')}` +
    (grants.length ? `, and may read ${grants.join(', ')}` : '');
  v.append(el('p', 'note',
    `This conversation runs on ${runs}, fixed for its life. None of it can be changed here: ` +
    `a different configuration is a different conversation, which Fork makes, leaving this one as it is.`));

  // The title is the exception to the paragraph above: nothing in the prompt
  // reads it, so it can be changed in place instead of by forking.
  v.append(el('h2', null, 'title'));
  const name = el('input', 'text');
  name.value = s.title || '';
  name.placeholder = 'What this conversation is about';
  v.append(name);
  const rename = el('button', 'btn', 'Rename');
  rename.onclick = async () => {
    const chosen = name.value.trim();
    // A conversation with no title is harder to find again than one with a
    // title that has gone stale, so a blank is not a rename.
    if (!chosen || chosen === (s.title || '')) return;
    try { await patch('/sessions/' + id, { title: chosen }); }
    catch (e) { toast({ title: 'Not renamed', body: String(e.message) }); return; }
    s.title = chosen;
    if (state.session && state.session.id === id) state.session.title = chosen;
    renderSidebar();
    toast({ title: 'Renamed', body: chosen });
  };
  v.append(rename);

  await configFacts(v, s);

  v.append(el('h2', null, 'danger'));
  const d = el('button', 'btn danger', 'Delete conversation');
  d.onclick = async () => { if (confirm('Delete this conversation, its transcript, and its jobs?')) { await del('/sessions/' + id); location.hash = '#sessions'; } };
  v.append(d);
}

// ---------- panels ----------

async function viewToolPanel(v) {
  const gen = drawing;
  setHeader(state.arg, true);
  try {
    const mod = await import('/tools/' + state.arg + '/panel.js');
    if (stale(gen)) return;
    await mod.default({ root: v, api, el });
  } catch (e) {
    v.append(el('div', 'empty', 'Panel failed to load: ' + e.message));
  }
}

async function viewPanels(v) {
  const gen = drawing;
  setHeader('Panels', true);
  const items = [
    ['jobs', 'Jobs', 'Schedules attached to conversations'],
    ['tools', 'Tools', 'Loaded tools and failures'],
    ['skills', 'Skills', 'What the agent knows how to do'],
    ['personas', 'Personas', 'Who the agent is when a conversation starts'],
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
    if (stale(gen)) return;
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
  await versionSection(v);
}

// Which build is answering, and what changed in it. The version is the commit
// the image was built from, so what is on screen is what a rollback names. Both
// are read once after an upgrade and never again, so they sit at the foot of
// this panel and the changelog stays folded until it is asked for.
async function versionSection(v) {
  let info;
  try { info = await api('/version'); } catch (e) { return; }
  if (!info) return;
  v.append(el('h2', null, 'version'));
  const built = info.built_at ? ' · built ' + ago(info.built_at) : '';
  v.append(el('div', 's', (info.version || 'unknown') + built));
  if (!info.changelog) {
    v.append(el('div', 's', 'No changelog shipped with this build.'));
    return;
  }
  const log = el('div', 'changelog md');
  log.hidden = true;
  log.innerHTML = renderMarkdown(info.changelog);
  const toggle = el('button', 'btn', 'What changed');
  toggle.onclick = () => { log.hidden = !log.hidden; };
  v.append(toggle, log);
}

async function viewJobs(v) {
  const gen = drawing;
  setHeader('Jobs', true);
  const scope = state.arg ? '&session_id=' + state.arg : '';
  const jobs = await api('/jobs' + (state.arg ? '?session_id=' + state.arg : ''));
  if (stale(gen)) return;
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



async function viewTools(v) {
  const gen = drawing;
  setHeader('Tools', true);
  const data = await api('/tools');
  if (stale(gen)) return;
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
    m.append(reachList(t));
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

// reachList is what a tool may touch, as the sandbox applies it: the files it
// may read and write, and the ports it may open. A boundary that is not
// enforced here says so first, and then what it would be where it is. What
// every tool gets is set apart from what this tool's manifest asked for, because
// the first is the same on every card and the second is a decision about one.
function reachList(t) {
  const box = el('div', 'reach');
  const list = parent => {
    const dl = el('dl', 'settings');
    parent.append(dl);
    return (k, val) => dl.append(el('dt', null, k), el('dd', null, val));
  };
  const group = (cls, title) => {
    const g = el('div', 'reach-group ' + cls);
    g.append(el('div', 'reach-scope', title));
    box.append(g);
    return list(g);
  };
  if (t.builtin || !t.reach) {
    list(box)('runs', 'inside the agent — no sandbox applies');
    return box;
  }
  const r = t.reach;
  if (!r.enforced) list(box)('boundary', `NOT ENFORCED here (${r.reason || 'no sandbox'}). Where it is, this applies:`);
  const read = r.read || [], own = r.tool_read || [];
  const field = group('reach-every', 'Every tool');
  field('read & write', (r.read_write || []).join(', '));
  // The policy lists what the tool asked for after everything else.
  field('read', read.slice(0, read.length - own.length).join(', '));
  if ((r.files || []).length) field('files', r.files.join(', '));
  const ports = (r.ports || []).filter(p => p !== r.tool_api_port);
  let net = 'TCP to ports ' + ports.join(', ');
  if (r.tool_api_port) net += '; the agent through its tool API';
  if (r.operator_port) net += '; never the operator port ' + r.operator_port;
  if (r.enforced && !r.network_enforced) net = `NOT ENFORCED (${r.network_reason || 'no network rules'}); would be ${net}`;
  field('network', net);
  const mine = group('reach-own', 'This tool only');
  const env = r.tool_env || [];
  if (own.length) mine('read', own.join(', '));
  // The name of a credential, never its value: what the card is for is saying
  // which tool holds one, so that a tool holding one cannot do so quietly.
  if (env.length) mine('environment', env.join(', '));
  if (!own.length && !env.length) mine('adds', 'nothing beyond what every tool gets');
  return box;
}

// A skill and a persona are the same thing on disk: a Markdown file with
// frontmatter that the operator writes. So they are listed, written, and deleted
// through one screen, and only the wording differs.
const AUTHORED = {
  skills: {
    path: '/skills', list: 'skills', one: 'skill',
    blurb: 'Prose that teaches the agent how to do one thing. Only the name and description reach the ' +
      'system prompt; the agent reads the body when it judges the skill relevant. A skill you add here ' +
      'is in the conversations started after it, not the ones already running.',
  },
  personas: {
    path: '/personas', list: 'personas', one: 'persona',
    blurb: 'The opening section of the system prompt. Choosing one for a conversation replaces the ' +
      'built-in persona whole — including its working rules about scheduling, unattended wakes, and ' +
      'what the agent cannot do — so write in the ones you want kept.',
  },
};

async function viewAuthored(v, kind) {
  const gen = drawing;
  const spec = AUTHORED[kind];
  const title = kind === 'skills' ? 'Skills' : 'Personas';
  // Each document, and a new one, has an address of its own, so the back button
  // returns to this list. A name holds no '+', so '+new' is never a document.
  if (state.arg) return editAuthored(kind, state.arg === '+new' ? null : decodeURIComponent(state.arg));
  setHeader(title, true, [{ label: 'New ' + spec.one, fn: () => location.hash = '#' + kind + '/+new' }]);
  const data = await api(spec.path);
  if (stale(gen)) return;
  v.append(el('p', 'note', spec.blurb));
  const grid = el('div', 'cards');
  v.append(grid);
  for (const f of (data.failures || [])) {
    const row = el('div', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', f.dir), el('div', 's', f.reason), el('span', 'tag bad', 'skipped'));
    row.append(m); grid.append(row);
  }
  const items = data[spec.list] || [];
  if (!items.length && !(data.failures || []).length) v.append(el('div', 'empty', 'Nothing on disk.'));
  for (const s of items) {
    const row = el('button', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', s.name), el('div', 's', s.description));
    const tags = el('div');
    tags.append(el('span', 'tag', s.bytes + ' bytes'));
    if (!s.editable) tags.append(el('span', 'tag', 'ships with the agent'));
    if (kind === 'skills' && s.default_enabled === false) tags.append(el('span', 'tag', 'off unless chosen'));
    m.append(tags);
    row.append(m);
    row.onclick = () => location.hash = '#' + kind + '/' + encodeURIComponent(s.name);
    grid.append(row);
  }
}

function viewSkills(v) { return viewAuthored(v, 'skills'); }
function viewPersonas(v) { return viewAuthored(v, 'personas'); }

// editAuthored opens one document for reading, and for writing when it is the
// operator's. What ships in the image is read-only: writing a copy would shadow
// the original until the next deployment brought it back, which is the one state
// nothing on screen could explain.
async function editAuthored(kind, name) {
  const spec = AUTHORED[kind];
  const doc = name ? await api(spec.path + '/' + encodeURIComponent(name)) : null;
  const v = $('view');
  v.innerHTML = '';
  const editable = !doc || doc.editable;

  if (doc && !editable) {
    setHeader(doc.name, true);
    v.append(el('p', 'note', 'This ' + spec.one + ' ships with the agent. It is read-only here; ' +
      'copy it under another name to write your own.'));
    v.append(el('div', 's', doc.description));
    const pre = el('pre', null, doc.body);
    pre.style.whiteSpace = 'pre-wrap'; pre.style.fontSize = '13px';
    v.append(pre);
    return;
  }

  setHeader(doc ? doc.name : 'New ' + spec.one, true, doc ? [{
    label: 'Delete', danger: true, fn: async () => {
      try { await del(spec.path + '/' + encodeURIComponent(doc.name)); backToList(kind); }
      catch (e) { toast({ title: 'Not deleted', body: String(e.message) }); }
    }
  }] : null);

  v.append(el('h2', null, 'name'));
  const nameInput = el('input', 'text');
  nameInput.placeholder = 'one-word-name';
  if (doc) { nameInput.value = doc.name; nameInput.disabled = true; }
  v.append(nameInput);

  v.append(el('h2', null, 'description'));
  v.append(el('p', 'note', kind === 'skills'
    ? 'One line. This is what the agent reads in every prompt to decide whether to open the skill.'
    : 'One line, so you can tell them apart when you start a conversation.'));
  const desc = el('input', 'text');
  if (doc) desc.value = doc.description;
  v.append(desc);

  v.append(el('h2', null, 'body'));
  const body = el('textarea', 'text');
  body.rows = 20;
  if (doc) body.value = doc.body;
  v.append(body);

  let on = doc ? doc.default_enabled !== false : true;
  if (kind === 'skills') {
    v.append(el('h2', null, 'by default'));
    v.append(el('p', 'note', 'A skill that is on by default is indexed in every conversation that did ' +
      'not choose its skills. Turn it off for one a conversation should set out to use.'));
    const toggle = el('button', 'pill' + (on ? ' on' : ''), on ? 'on by default' : 'off unless chosen');
    toggle.onclick = () => {
      on = !on;
      toggle.className = 'pill' + (on ? ' on' : '');
      toggle.textContent = on ? 'on by default' : 'off unless chosen';
    };
    v.append(toggle);
  }

  const save = el('button', 'btn primary', 'Save');
  save.onclick = async () => {
    const payload = { name: nameInput.value.trim(), description: desc.value.trim(), body: body.value };
    if (kind === 'skills') payload.default_enabled = on;
    try {
      if (doc) {
        await api(spec.path + '/' + encodeURIComponent(doc.name), {
          method: 'PUT', headers: { 'content-type': 'application/json' },
          body: JSON.stringify(payload)
        });
      } else {
        await post(spec.path, payload);
      }
      backToList(kind);
    } catch (e) {
      toast({ title: 'Not saved', body: String(e.message) });
    }
  };
  const done = el('div', 'finish'); done.append(save);
  v.append(done);
}

// backToList leaves an editor for its list: back, when the list is where the
// editor was opened from, and otherwise the list in the editor's place, so the
// history never holds the list twice or an editor for something just saved.
function backToList(kind) {
  const list = '#' + kind;
  if (state.from === list) history.back();
  else location.replace(list);
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
