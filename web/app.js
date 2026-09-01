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
const fmtMoney = v => '$' + (v || 0).toFixed(4);
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

const state = { view: null, session: null, entries: [], streaming: '', sessions: [], models: [], status: null, collapsed: {} };

// ---------- routing ----------

function route() {
  const h = location.hash.slice(1) || 'sessions';
  const [name, arg] = h.split('/');
  state.view = name;
  state.arg = arg;
  render();
}
window.addEventListener('hashchange', route);

$('back').onclick = () => history.length > 1 ? history.back() : (location.hash = '#sessions');
$('panels').onclick = () => location.hash = '#panels';

// ---------- websocket ----------

let ws;
function connect() {
  ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws');
  ws.onmessage = ev => handle(JSON.parse(ev.data));
  ws.onclose = () => setTimeout(connect, 2000);
}
function handle(e) {
  if (e.kind === 'entry' || e.kind === 'transient') {
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
  } else if (e.kind === 'notification') {
    toast(e.notification);
    if (state.view === 'session') loadStatus();
  } else if (e.kind === 'sessions' || e.kind === 'jobs' || e.kind === 'dead_letters' || e.kind === 'status') {
    if (['sessions', 'jobs', 'dead', 'panels'].includes(state.view)) render();
    if (e.kind === 'status') loadStatus();
  }
}

function toast(n) {
  if (!n) return;
  const t = el('div', 'toast');
  t.append(el('b', null, n.title || ''), el('span', null, n.body || ''));
  t.onclick = () => { t.remove(); if (n.session_id) location.hash = '#session/' + n.session_id; };
  document.body.append(t);
  setTimeout(() => t.remove(), 8000);
}

// ---------- shell ----------

function setHeader(title, showBack) {
  $('title').textContent = title;
  $('back').hidden = !showBack;
}

function render() {
  $('foot').innerHTML = '';
  $('tabs').hidden = true;
  const v = $('view');
  v.innerHTML = '';
  switch (state.view) {
    case 'session': return viewSession(v);
    case 'panels': return viewPanels(v);
    case 'jobs': return viewJobs(v);
    case 'dead': return viewDead(v);
    case 'memory': return viewMemory(v);
    case 'tools': return viewTools(v);
    case 'skills': return viewSkills(v);
    case 'search': return viewSearch(v);
    case 'settings': return viewSettings(v);
    case 'toolpanel': return viewToolPanel(v);
    default: return viewSessions(v);
  }
}

// ---------- sessions list ----------

async function viewSessions(v) {
  setHeader('agent', false);
  const bar = el('div');
  const nw = el('button', 'act primary', '+ new conversation');
  nw.onclick = async () => { const s = await post('/sessions', {}); location.hash = '#session/' + s.id; };
  const search = el('button', 'act', 'search');
  search.onclick = () => location.hash = '#search';
  bar.append(nw, search);
  v.append(bar);

  const list = await api('/sessions');
  state.sessions = list;
  const active = list.filter(s => s.status === 'active');
  const archived = list.filter(s => s.status !== 'active');
  const section = (label, items) => {
    if (!items.length) return;
    v.append(el('h2', null, label));
    items.forEach(s => v.append(sessionRow(s)));
  };
  section('active', active);
  section('archived', archived);
  if (!list.length) v.append(el('div', 'empty', 'No conversations yet.'));
}

function sessionRow(s) {
  const row = el('button', 'row-item');
  const m = el('div', 'm');
  m.append(el('div', 'n', s.title || 'Untitled'));
  const sub = el('div', 's');
  sub.textContent = `${s.model.split('/').pop()} · ${s.entry_count} entries · ${s.context_used.toLocaleString()}/${s.rotate_at_tokens.toLocaleString()} tok · ${fmtMoney(s.cost)} · ${ago(s.last_active_at)}`;
  m.append(sub);
  if (s.job_count) { const t = el('span', 'tag on', s.job_count + ' job' + (s.job_count > 1 ? 's' : '')); m.append(t); }
  if (s.continued_by) m.append(el('span', 'tag', '→ continued'));
  if (s.muted) m.append(el('span', 'tag', 'muted'));
  row.append(m);
  if (s.unread) row.append(el('span', 'badge', String(s.unread)));
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
  setHeader(state.session.title || 'Conversation', true);
  await post('/sessions/' + state.arg + '/read', {});

  const controls = el('div');
  const mk = (label, fn, cls) => { const b = el('button', 'act ' + (cls || ''), label); b.onclick = fn; return b; };
  controls.append(
    mk('controls', () => location.hash = '#settings/' + state.session.id),
    mk('jobs', () => location.hash = '#jobs/' + state.session.id),
    mk('summary', showSummary),
    mk('fork', async () => { const s = await post('/sessions/' + state.session.id + '/fork', { archive: false }); location.hash = '#session/' + s.id; }),
    mk(state.session.muted ? 'unmute' : 'mute', async () => { await patch('/sessions/' + state.session.id, { muted: !state.session.muted }); render(); })
  );
  v.append(controls);

  const t = el('div'); t.id = 'transcript';
  v.append(t);
  renderTranscript();

  const foot = $('foot');
  const status = el('div', 'status'); status.id = 'statusline';
  const form = el('form', 'composer');
  const ta = el('textarea'); ta.rows = 1; ta.placeholder = 'Message';
  ta.oninput = () => { ta.style.height = 'auto'; ta.style.height = Math.min(ta.scrollHeight, 140) + 'px'; };
  ta.onkeydown = e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); form.requestSubmit(); } };
  const up = el('button', null, '＋'); up.type = 'button';
  const file = el('input'); file.type = 'file'; file.hidden = true;
  up.onclick = () => file.click();
  file.onchange = async () => {
    if (!file.files.length) return;
    const fd = new FormData();
    fd.append('file', file.files[0]);
    fd.append('session_id', state.session.id);
    try { const r = await api('/uploads', { method: 'POST', body: fd }); toast({ title: 'Uploaded', body: r.path }); }
    catch (e) { toast({ title: 'Upload failed', body: String(e.message) }); }
    file.value = '';
  };
  const send = el('button', null, 'send'); send.type = 'submit';
  form.append(up, file, ta, send);
  form.onsubmit = async e => {
    e.preventDefault();
    const text = ta.value.trim();
    if (!text) return;
    ta.value = ''; ta.style.height = 'auto';
    await post('/sessions/' + state.session.id + '/messages', { text });
  };
  foot.append(status, form);
  renderStatus();
  scrollDown();
}

function renderStatus() {
  const s = state.session;
  const line = $('statusline');
  if (!line || !s) return;
  line.innerHTML = '';
  const pct = Math.min(100, 100 * s.context_used / s.rotate_at_tokens);
  const bar = el('span', 'bar'); const fill = el('i'); fill.style.width = pct + '%'; bar.append(fill);
  const add = (label, val) => { const w = el('span'); w.append(document.createTextNode(label + ' ')); w.append(el('b', null, val)); line.append(w); };
  add('model', s.model.split('/').pop());
  line.append(bar);
  add('', `${s.context_used.toLocaleString()} / ${s.rotate_at_tokens.toLocaleString()} tok`);
  add('cost', fmtMoney(s.cost));
  if (s.cache_hit_rate) add('cached', Math.round(s.cache_hit_rate * 100) + '%');
  const last = [...state.entries].reverse().find(e => e.usage && e.usage.tokens_per_second);
  if (last) add('', last.usage.tokens_per_second.toFixed(1) + ' tok/s');
  if (state.streaming) line.append(el('span', null, '· generating'));
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
  n.querySelector('.bub').textContent = state.streaming;
  scrollDown();
}

function streamNode() {
  const w = el('div', 'msg'); w.id = 'streaming';
  w.append(el('div', 'who', 'agent'), el('div', 'bub', state.streaming));
  return w;
}

function renderEntry(e) {
  if (e.event_kind === 'prompt') return promptEntry(e);
  if (e.type === 'event') {
    const bad = ['job_error', 'dead_letter', 'error'].includes(e.event_kind);
    const n = el('div', 'ev' + (e.job_id ? ' job' : '') + (bad ? ' bad' : ''));
    const kind = (e.event_kind || 'event').replace(/_/g, ' ');
    n.textContent = `${kind}${e.status ? ' · ' + e.status : ''} · ${e.text || ''}`;
    n.title = new Date(e.created_at).toLocaleString();
    return n;
  }
  if (e.role === 'tool') {
    let res = {}; try { res = JSON.parse(e.tool_result || '{}'); } catch (err) {}
    const n = el('div', 'tool' + (res.ok === false ? ' err' : ''));
    const head = el('div', 't', '← ' + e.tool_name);
    head.style.cursor = 'pointer';
    const pre = el('pre', null, res.ok === false ? (res.error || '') : (res.content || ''));
    pre.hidden = true;
    head.onclick = () => pre.hidden = !pre.hidden;
    n.append(head, pre);
    return n;
  }
  const w = el('div', 'msg ' + (e.role === 'user' ? 'you' : '') + (e.carried_from ? ' carried' : ''));
  if (e.job_id) {
    const a = el('span', 'attr', 'job ' + e.job_id.slice(-6));
    a.onclick = () => location.hash = '#jobs';
    w.append(a);
  }
  if (e.carried_from) w.append(el('span', 'attr', 'carried from ' + e.carried_from.slice(-6)));
  w.append(el('div', 'who', e.role === 'user' ? 'you' : 'agent'));
  if (e.text) w.append(el('div', 'bub', e.text));
  for (const c of (e.tool_calls || [])) {
    const n = el('div', 'tool');
    const head = el('div', 't', '→ ' + c.name);
    head.style.cursor = 'pointer';
    const pre = el('pre', null, c.arguments);
    pre.hidden = true;
    head.onclick = () => pre.hidden = !pre.hidden;
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
    if (open) sec.append(el('pre', null, s.text));
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
  const ta = el('textarea'); ta.style.width = '100%'; ta.style.minHeight = '300px';
  ta.className = 'text'; ta.value = s.summary || '(no summary yet — this session has not grown past the threshold)';
  const save = el('button', 'act primary', 'save');
  save.onclick = async () => { await patch('/sessions/' + s.id, { summary: ta.value }); location.hash = '#session/' + s.id; };
  v.append(ta, save);
}

// ---------- per-session controls ----------

async function viewSettings(v) {
  const id = state.arg;
  const res = await api('/sessions/' + id);
  const s = res.session;
  setHeader('Controls', true);

  v.append(el('h2', null, 'model'));
  const sel = el('select', 'text');
  const models = state.models.length ? state.models : (state.models = await api('/models').catch(() => []));
  const opts = models.length ? models : [{ id: s.model, name: s.model }];
  for (const m of opts) {
    const o = el('option', null, `${m.id}${m.context_length ? '  · ' + Math.round(m.context_length / 1000) + 'k' : ''}`);
    o.value = m.id;
    if (m.id === s.model) o.selected = true;
    sel.append(o);
  }
  sel.onchange = async () => { await patch('/sessions/' + id, { model: sel.value }); toast({ title: 'Model changed', body: sel.value }); };
  v.append(sel);

  const toggles = async (label, path, current, setter) => {
    const data = await api(path + '?session_id=' + id);
    const items = data.tools || data.skills;
    v.append(el('h2', null, label));
    const bar = el('div');
    const all = el('button', 'act', 'enable all');
    const none = el('button', 'act', 'disable all');
    all.onclick = async () => { await setter(null); render(); };
    none.onclick = async () => { await setter([]); render(); };
    bar.append(all, none);
    v.append(bar);
    const wrap = el('div');
    for (const it of items) {
      const p = el('button', 'pill' + (it.enabled ? ' on' : ''), it.name);
      p.onclick = async () => {
        const enabled = items.filter(x => x.enabled).map(x => x.name);
        const next = it.enabled ? enabled.filter(n => n !== it.name) : enabled.concat(it.name);
        await setter(next);
        render();
      };
      wrap.append(p);
    }
    v.append(wrap);
  };
  await toggles('tools', '/tools', s.enabled_tools, val => patch('/sessions/' + id, { enabled_tools: val }));
  await toggles('skills', '/skills', s.enabled_skills, val => patch('/sessions/' + id, { enabled_skills: val }));

  v.append(el('h2', null, 'danger'));
  const d = el('button', 'act', 'delete conversation');
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
    ['dead', 'Dead letters', 'Jobs that gave up'],
    ['memory', 'Memory', 'What survives a conversation'],
    ['tools', 'Tools', 'Loaded tools and failures'],
    ['skills', 'Skills', 'What the agent knows how to do'],
    ['search', 'Search', 'Full text across every transcript'],
  ];
  for (const [hash, name, sub] of items) {
    const row = el('button', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', name), el('div', 's', sub));
    row.append(m);
    row.onclick = () => location.hash = '#' + hash;
    v.append(row);
  }
  try {
    const data = await api('/tools');
    const withPanel = data.tools.filter(t => t.has_panel);
    if (withPanel.length) v.append(el('h2', null, 'tool panels'));
    for (const t of withPanel) {
      const row = el('button', 'row-item');
      const m = el('div', 'm');
      m.append(el('div', 'n', t.name), el('div', 's', t.description));
      row.append(m);
      row.onclick = () => location.hash = '#toolpanel/' + t.name;
      v.append(row);
    }
  } catch (e) {}
  if (state.status && state.status.key) {
    v.append(el('h2', null, 'openrouter'));
    const k = state.status.key;
    v.append(el('div', 's', `usage ${fmtMoney(k.usage)}${k.remaining != null ? ' · remaining ' + fmtMoney(k.remaining) : ''}`));
  }
  const push = el('button', 'act primary', 'enable notifications');
  push.onclick = enablePush;
  v.append(el('h2', null, 'device'), push);
}

async function viewJobs(v) {
  setHeader('Jobs', true);
  const jobs = await api('/jobs' + (state.arg ? '?session_id=' + state.arg : ''));
  if (!jobs || !jobs.length) { v.append(el('div', 'empty', 'No jobs scheduled.')); return; }
  for (const j of jobs) {
    const row = el('div', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', j.prompt));
    m.append(el('div', 's', `${j.schedule} · next ${until(j.next_run_at)} · expires ${until(j.expires_at)} · ${j.run_count} runs`));
    if (j.kind === 'check') m.append(el('div', 's', 'check: ' + j.check));
    else if (j.kind === 'due') m.append(el('span', 'tag', 'reminder · no condition, fires when due'));
    else m.append(el('span', 'tag warn', 'judgement · one model call per tick'));
    if (j.last_status) m.append(el('span', 'tag ' + (j.last_status === 'fired' ? 'ok' : ''), j.last_status));
    const acts = el('div');
    const open = el('button', 'act', 'open');
    open.onclick = () => location.hash = '#session/' + j.session_id;
    const rm = el('button', 'act', 'delete');
    rm.onclick = async () => { await del('/jobs/' + j.id); render(); };
    acts.append(open, rm);
    m.append(acts);
    row.append(m);
    v.append(row);
  }
}

async function viewDead(v) {
  setHeader('Dead letters', true);
  const list = await api('/dead-letters');
  const open = (list || []).filter(d => d.status === 'open');
  if (!open.length) { v.append(el('div', 'empty', 'Nothing gave up.')); }
  for (const d of (list || [])) {
    const row = el('div', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', d.job_spec.prompt));
    m.append(el('div', 's', `${d.reason} · ${d.detail || ''} · ${d.run_count} runs · ${ago(d.created_at)}`));
    m.append(el('span', 'tag ' + (d.status === 'open' ? 'bad' : ''), d.status));
    if (d.status === 'open') {
      const acts = el('div');
      const rep = el('button', 'act primary', 'replay');
      rep.onclick = async () => { await post('/dead-letters/' + d.id + '/replay', {}); render(); };
      const dis = el('button', 'act', 'dismiss');
      dis.onclick = async () => { await post('/dead-letters/' + d.id + '/dismiss', {}); render(); };
      acts.append(rep, dis);
      m.append(acts);
    }
    row.append(m);
    v.append(row);
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
    const rm = el('button', 'act', 'delete');
    rm.onclick = async () => { await del('/memory/' + m.id); render(); };
    wrap.append(rm);
    row.append(wrap);
    v.append(row);
  }
  const add = el('input', 'text'); add.placeholder = 'remember one fact…';
  add.onchange = async () => {
    try { await post('/memory', { text: add.value }); render(); }
    catch (e) { toast({ title: 'Memory is full', body: String(e.message) }); }
  };
  v.append(el('h2', null, 'add'), add);
}

async function viewTools(v) {
  setHeader('Tools', true);
  const data = await api('/tools');
  const rl = el('button', 'act primary', 'reload from disk');
  rl.onclick = async () => { const r = await post('/tools/reload', {}); toast({ title: 'Reloaded', body: (r.loaded || []).join(', ') }); render(); };
  v.append(rl);
  for (const f of (data.failures || [])) {
    const row = el('div', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', f.dir), el('div', 's', f.reason));
    m.append(el('span', 'tag bad', 'not loaded'));
    row.append(m);
    v.append(row);
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
    const sh = el('button', 'act', 'manifest');
    sh.onclick = () => pre.hidden = !pre.hidden;
    m.append(sh, pre);
    row.append(m);
    v.append(row);
  }
}

async function viewSkills(v) {
  setHeader('Skills', true);
  const data = await api('/skills');
  for (const f of (data.failures || [])) {
    const row = el('div', 'row-item');
    const m = el('div', 'm');
    m.append(el('div', 'n', f.dir), el('div', 's', f.reason), el('span', 'tag bad', 'skipped'));
    row.append(m); v.append(row);
  }
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
    v.append(row);
  }
}

async function viewSearch(v) {
  setHeader('Search', true);
  const inp = el('input', 'text'); inp.placeholder = 'search every transcript';
  const out = el('div');
  inp.onchange = async () => {
    out.innerHTML = '';
    if (!inp.value.trim()) return;
    let hits = [];
    try { hits = await api('/search?q=' + encodeURIComponent(inp.value)) || []; }
    catch (e) { out.append(el('div', 'empty', String(e.message))); return; }
    if (!hits.length) { out.append(el('div', 'empty', 'No matches.')); return; }
    for (const h of hits) {
      const row = el('button', 'row-item');
      const m = el('div', 'm');
      m.append(el('div', 'n', h.title || h.session_id));
      m.append(el('div', 's', h.snippet));
      row.append(m);
      row.onclick = () => location.hash = '#session/' + h.session_id;
      out.append(row);
    }
  };
  v.append(inp, out);
  inp.focus();
}

// ---------- push ----------

async function enablePush() {
  try {
    if (!('serviceWorker' in navigator)) throw new Error('no service worker support');
    const reg = await navigator.serviceWorker.register('/sw.js');
    const perm = await Notification.requestPermission();
    if (perm !== 'granted') throw new Error('permission ' + perm);
    const { public_key } = await api('/push/key');
    if (!public_key) throw new Error('server has no VAPID key');
    const sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlB64(public_key)
    });
    await post('/push/subscriptions', sub.toJSON());
    toast({ title: 'Notifications on', body: 'This device will be interrupted for dead letters and notify calls.' });
  } catch (e) {
    toast({ title: 'Notifications unavailable', body: String(e.message) });
  }
}

function urlB64(s) {
  const pad = '='.repeat((4 - s.length % 4) % 4);
  const b = atob((s + pad).replace(/-/g, '+').replace(/_/g, '/'));
  return Uint8Array.from([...b].map(c => c.charCodeAt(0)));
}

// ---------- boot ----------

async function boot() {
  if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});
  connect();
  route();
  try {
    state.status = await api('/status');
    const b = state.status.breaker;
    $('breaker').className = b && b.open ? 'breaker' : '';
    $('breaker').textContent = b && b.open ? 'Scheduler paused: ' + b.reason : '';
  } catch (e) {
    $('breaker').className = 'breaker';
    $('breaker').textContent = 'Cannot reach the gateway. Are you on the tailnet?';
  }
}
boot();
