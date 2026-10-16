'use strict';

// Pure helpers for rendering a transcript entry. Kept out of app.js so they can
// be tested without a DOM; app.js loads this file first and uses the globals.

// toolResult normalises the tool_result field of a transcript entry. The API
// sends it as a JSON object, but a result may also arrive as a JSON string, or
// as plain text from a tool that never wrapped it. All three must render: a
// result that renders as nothing hides a tool's whole answer from the operator.
function toolResult(r) {
  if (r == null) return {};
  if (typeof r !== 'string') return r;
  try {
    const parsed = JSON.parse(r);
    return parsed && typeof parsed === 'object' ? parsed : { content: r };
  } catch (e) {
    return { content: r };
  }
}

// resultBody is the text to show for a result: the error when the call failed,
// the content when it succeeded, and an explicit marker when a call genuinely
// returned nothing, so silence is never mistaken for a broken transcript.
function resultBody(res) {
  const body = res.ok === false ? res.error : res.content;
  return body == null || body === '' ? '(no output)' : body;
}

// clip is the shape of every collapsed hint: the first line, trimmed, and short
// enough to sit in a header.
function clip(text) {
  const line = String(text || '').split('\n')[0].trim();
  return line.length > 72 ? line.slice(0, 72) + '…' : line;
}

// resultPreview is the one-line hint shown in the collapsed header.
function resultPreview(body) {
  return clip(body);
}

// ---- calls ----

// A call and its result are two halves of one exchange, so both preview what
// they carry and both say when they carry nothing.

// callArgs parses a call's arguments. The API sends them as a JSON string, and a
// model can send something that is not JSON at all.
function callArgs(args) {
  if (args == null || args === '') return null;
  if (typeof args === 'object') return args;
  try {
    const parsed = JSON.parse(args);
    return parsed && typeof parsed === 'object' ? parsed : null;
  } catch (e) {
    return null;
  }
}

// callBody is what an expanded call shows: its arguments, indented when they are
// JSON, and an explicit marker when there were none.
function callBody(args) {
  const parsed = callArgs(args);
  if (parsed) return Object.keys(parsed).length ? JSON.stringify(parsed, null, 2) : '(no arguments)';
  const raw = String(args == null ? '' : args).trim();
  return raw === '' ? '(no arguments)' : raw;
}

// callPreview is the one-line hint for a collapsed call: what it asked for,
// as key=value.
function callPreview(args) {
  const parsed = callArgs(args);
  if (!parsed) return clip(args);
  const parts = Object.entries(parsed).map(([k, v]) =>
    k + '=' + (typeof v === 'string' ? v : JSON.stringify(v)));
  return clip(parts.join(' \u00b7 ').replace(/\s+/g, ' '));
}

// ---- events ----

// An event is a log line the system wrote, not something anyone said. These
// three shape it into time / kind / detail so it reads as a log at a glance and
// is never mistaken for a message or a tool call.

const FAILURES = ['job_error', 'error'];

function eventLabel(e) {
  return String((e && e.event_kind) || 'event').replace(/_/g, ' ').toUpperCase();
}

function eventDetail(e) {
  const parts = [];
  if (e && e.status) parts.push(String(e.status).replace(/_/g, ' '));
  if (e && e.text) parts.push(e.text);
  return parts.join(' \u00b7 ');
}

function eventTime(iso) {
  const d = new Date(iso);
  if (!iso || isNaN(d)) return '--:--:--';
  const pad = n => String(n).padStart(2, '0');
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// A tick that did not fire is the ordinary case, not a fault. Colour is spent
// only on a genuine failure, so it still means something when it appears.
function isFailure(e) {
  return FAILURES.includes((e && e.event_kind) || '');
}

// Who a message entry is attributed to. A job wake is stored with role "user"
// because that is the only role the model API takes for a turn's input, but
// nobody typed it: the scheduler replayed the job's own prompt. Calling that
// "you" tells the operator they said something they never said, so it is named
// for the job that woke it instead.
function speaker(e) {
  if (!e || e.role !== 'user') return 'agent';
  if (!e.job_id) return 'you';
  return 'job (' + e.job_id.slice(-6) + ')';
}

// The grey bubble reads as "you said this", so only what the operator typed
// gets it. A wake is set apart instead of dressed up as them.
function bubbleClass(e) {
  if (!e || e.role !== 'user') return '';
  return e.job_id ? 'job' : 'you';
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
                'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

// messageTime is the stamp beside a message. A conversation here can run for
// days, so a bare clock is ambiguous; the date appears only once the message
// stops being today's, which keeps the common case to five characters.
function messageTime(iso, now) {
  const d = new Date(iso);
  if (!iso || isNaN(d)) return '';
  const pad = n => String(n).padStart(2, '0');
  const clock = pad(d.getHours()) + ':' + pad(d.getMinutes());
  const today = now || new Date();
  const sameDay = d.getFullYear() === today.getFullYear()
    && d.getMonth() === today.getMonth()
    && d.getDate() === today.getDate();
  return sameDay ? clock : `${d.getDate()} ${MONTHS[d.getMonth()]} ${clock}`;
}

// runTime stamps a line of a job's log. Unlike a message, a run is read to
// answer "did this happen, and when?", so the date is always there.
function runTime(iso) {
  const d = new Date(iso);
  if (!iso || isNaN(d)) return '';
  const pad = n => String(n).padStart(2, '0');
  return `${d.getDate()} ${MONTHS[d.getMonth()]} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// lateBy is the gap between when a wake was due and when it ran. It is the
// visible trace of a host that slept, so it is said where the operator is
// already looking. Under a minute is the ordinary case and says nothing.
function lateBy(run) {
  if (!run || !run.due_at || !run.at) return '';
  const due = new Date(run.due_at), at = new Date(run.at);
  if (isNaN(due) || isNaN(at)) return '';
  const mins = Math.floor((at - due) / 60000);
  if (mins < 1) return '';
  if (mins < 60) return mins + 'm late';
  return `${Math.floor(mins / 60)}h ${mins % 60}m late`;
}

// jobCost is the price of a job in one line. The scheduler no longer refuses a
// cadence on the operator's behalf, so this is what they choose on: how often
// it wakes, what each wake sends, and what that comes to.
function jobCost(e) {
  if (!e || (!e.tokens_per_turn && !e.wakes_per_day)) return '';
  const n = v => Math.round(v || 0).toLocaleString();
  const money = v => '$' + (v >= 1 ? (v || 0).toFixed(2) : (v || 0).toFixed(4));
  if (e.gated) {
    return `gated by its check: no model cost until it passes, then ~${n(e.tokens_per_turn)} tok a turn`;
  }
  if (!e.wakes_per_day) return `one wake, ~${n(e.tokens_per_turn)} tok`;
  let out = `~${e.wakes_per_day.toFixed(1)} wakes a day · ~${n(e.tokens_per_day)} tok a day`;
  if (e.priced) out += ` · ~${money(e.cost_per_day)} a day, ~${money(e.cost_per_month)} a month`;
  return out;
}

// ---- session identifiers ----

// A session id is a ULID-ish run of uppercase hex, and it turns up as bare text
// wherever one conversation names another: a seeding event, a rotation line, the
// platform section of a prompt. Splitting it out lets the caller build a link
// without the DOM work leaking into a pure helper.
const SESSION_ID = /\b[0-9A-F]{20,22}\b/g;

function splitSessionIds(text) {
  const s = String(text == null ? '' : text);
  const parts = [];
  let at = 0;
  for (const m of s.matchAll(SESSION_ID)) {
    if (m.index > at) parts.push({ text: s.slice(at, m.index) });
    // A comparison's identifier has the shape of a session's and is not one:
    // the word before it is what tells them apart, and linked as a session it
    // would lead to a conversation that does not exist.
    const before = s.slice(at, m.index);
    const part = { text: m[0], id: m[0] };
    if (/comparison\s+$/i.test(before)) part.kind = 'comparison';
    parts.push(part);
    at = m.index + m[0].length;
  }
  if (at < s.length) parts.push({ text: s.slice(at) });
  return parts;
}

// ---- waiting ----

// waitingLabel is how long the agent has been working, as it ticks: seconds
// while that is all it is, then minutes and seconds, then hours and minutes.
function waitingLabel(seconds) {
  const s = Math.max(0, Math.floor(seconds || 0));
  const pad = n => String(n).padStart(2, '0');
  if (s < 60) return s + 's';
  if (s < 3600) return Math.floor(s / 60) + 'm ' + pad(s % 60) + 's';
  return Math.floor(s / 3600) + 'h ' + pad(Math.floor(s / 60) % 60) + 'm';
}

// ---- copying a conversation ----

const COPY_RESULT_LIMIT = 2000;

// clipped keeps a tool's output to a length someone will read, and says how much
// it dropped. A result that ends mid-sentence with nothing to mark the cut reads
// as a tool that stopped there.
function clipped(text) {
  const s = String(text == null ? '' : text);
  return s.length > COPY_RESULT_LIMIT
    ? s.slice(0, COPY_RESULT_LIMIT) + `\n… (${s.length - COPY_RESULT_LIMIT} more characters)`
    : s;
}

function copyStamp(iso) {
  const d = new Date(iso);
  if (!iso || isNaN(d)) return '';
  const pad = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// ---- reverting ----

// takenBack is every entry a revert took back: from the message each revert
// names up to the revert itself. They stay on disk, in search, and in the
// history; the agent no longer reads them, so the conversation does not draw
// them.
function takenBack(entries) {
  const gone = new Set();
  for (const r of entries || []) {
    if (r.type !== 'event' || r.event_kind !== 'revert') continue;
    for (const e of entries) if (e.seq >= r.reverts_from && e.seq < r.seq) gone.add(e.seq);
  }
  return gone;
}

// liveEntries is the conversation without what reverts took back.
function liveEntries(entries) {
  const gone = takenBack(entries);
  return gone.size ? (entries || []).filter(e => !gone.has(e.seq)) : (entries || []);
}

// conversationText is the whole conversation as Markdown, to paste somewhere
// else: every message in full, each call with its arguments, each result kept
// to a readable length, and each compaction's summary where it stands. What was
// said by neither party — the prompt and the event log — is left out. Folded
// turns are included: the conversation is what happened, not what the model
// can still see. What a revert took back is not: the operator took it back.
function conversationText(entries, title) {
  const out = ['# ' + (title || 'Conversation')];
  for (const e of liveEntries(entries)) {
    if (e.type === 'compaction') {
      out.push(`---\n\n*Compacted: ${e.folded_turns || 0} turns folded into this summary.*\n\n${e.text || ''}\n\n---`);
      continue;
    }
    if (e.type !== 'message') continue;
    if (e.role === 'tool') {
      const body = resultBody(toolResult(e.tool_result));
      const parts = ['← ' + (e.tool_name || 'tool') + '\n\n```\n' + clipped(body) + '\n```'];
      // The usual reason to copy a conversation carrying logs is that something
      // in it went wrong, so they travel with it rather than staying on screen.
      const logs = String(e.stderr || '').trim();
      if (logs) parts.push('logs\n\n```\n' + clipped(logs) + '\n```');
      out.push(parts.join('\n\n'));
      continue;
    }
    const who = e.role !== 'user' ? 'Agent' : e.job_id ? 'Job (' + e.job_id.slice(-6) + ')' : 'You';
    const parts = [`**${who}** · ${copyStamp(e.created_at)}`];
    if (e.text) parts.push(e.text);
    for (const c of e.tool_calls || []) parts.push('→ ' + c.name + '\n\n```json\n' + callBody(c.arguments) + '\n```');
    out.push(parts.join('\n\n'));
  }
  return out.join('\n\n') + '\n';
}

// transcriptRows lays a conversation out the way the agent reads it. Before any
// compaction that is everything, in order. After one it is the prompt, the
// newest summary standing for everything it covers, the turns that summary
// carried over word for word, and what was said since. A compaction is written
// after the turns it keeps, so those are found by sequence: drawn where the
// entry lies, they sat above the summary and read as the old turns it replaced.
// What it covers is not drawn at all; search finds it, and the history shows it.
// Nor is what a revert took back, which is read past before anything else, so a
// compaction it took back is not the one in force.
function transcriptRows(entries) {
  entries = liveEntries(entries);
  let c = -1;
  for (let i = entries.length - 1; i >= 0; i--) if (entries[i].type === 'compaction') { c = i; break; }
  if (c < 0) return entries.map(entry => ({ entry, part: 'live' }));
  const summary = entries[c];
  const covers = summary.covers_through || 0;
  let prompt = null;
  for (const e of entries) if (e.type === 'prompt') prompt = e;
  const rows = [];
  if (prompt) rows.push({ entry: prompt, part: 'live' });
  // A fork's seeding event is its own, written ahead of the copy, so a
  // compaction the copy carries covers it by position without standing for it.
  const seed = entries.find(e => e.type === 'event' && e.event_kind === 'forked_from');
  if (seed && seed.seq <= covers) rows.push({ entry: seed, part: 'live' });
  rows.push({ entry: summary, part: 'summary' });
  for (const e of entries) {
    if (e.seq <= covers || e.type === 'compaction' || e.type === 'prompt') continue;
    rows.push({ entry: e, part: e.seq < summary.seq ? 'carried' : 'live' });
  }
  return rows;
}

// cacheState is the live cache as the status line shows it: how much of the
// conversation the provider holds, and, where the agent's own marker chose the
// lifetime, how long it has left. A provider that caches on its own reports no
// lifetime, so none is counted. Null when nothing is known to be held.
function cacheState(cache, now) {
  if (!cache || !(cache.tokens > 0)) return null;
  if (cache.kept_by === 'provider') return { tokens: cache.tokens, live: true, by: 'provider' };
  const left = (new Date(cache.expires_at) - now) / 1000;
  return { tokens: cache.tokens, live: left > 0, left: Math.max(0, left), by: 'agent' };
}

// cacheLabel says what cacheState found, counting down in minutes and seconds.
function cacheLabel(c) {
  if (!c) return '';
  const size = c.tokens.toLocaleString() + ' tok';
  if (c.by === 'provider') return 'cache ' + size + ' · kept by the provider';
  if (!c.live) return 'cache expired';
  const secs = Math.ceil(c.left);
  return 'cache ' + size + ' · ' + Math.floor(secs / 60) + ':' + String(secs % 60).padStart(2, '0');
}

// nextCall estimates the next call: the prompt and the conversation as the
// agent would send them now, and what that costs at the model's prices. While
// the cache is live the part of it the cache holds is priced as a read.
function nextCall(promptTokens, contextUsed, cache, model) {
  const tokens = (promptTokens || 0) + (contextUsed || 0);
  const cached = !!(cache && cache.live);
  if (!model || model.prompt_price == null) return { tokens, cached, cost: null };
  const read = model.cache_read_price != null ? model.cache_read_price : model.prompt_price;
  const hit = cached ? Math.min(cache.tokens, tokens) : 0;
  return { tokens, cached, cost: hit * read + (tokens - hit) * model.prompt_price };
}

// cacheHit is what one call read from the cache, as the provider measured it,
// and says plainly when it read nothing. A lifetime is not a call's to show.
function cacheHit(usage) {
  if (!usage || !(usage.prompt_tokens > 0)) return '';
  if (!usage.cached_tokens) return 'no cache hit';
  return Math.round(100 * usage.cached_tokens / usage.prompt_tokens) + '% cached · ' +
    usage.cached_tokens.toLocaleString() + ' of ' + usage.prompt_tokens.toLocaleString() + ' tok';
}

// snippetParts splits a search snippet at the brackets the index puts around
// each match, so the page can mark them without reading the snippet as markup.
function snippetParts(snippet) {
  const out = [];
  const re = /\[([^\]]*)\]/g;
  let last = 0, m;
  const text = String(snippet || '');
  while ((m = re.exec(text))) {
    if (m.index > last) out.push({ text: text.slice(last, m.index), hit: false });
    out.push({ text: m[1], hit: true });
    last = re.lastIndex;
  }
  if (last < text.length) out.push({ text: text.slice(last), hit: false });
  return out;
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { toolResult, resultBody, resultPreview, callBody, callPreview,
    eventLabel, eventDetail, eventTime, isFailure,
    speaker, bubbleClass, messageTime, runTime, lateBy, jobCost, splitSessionIds,
    waitingLabel, conversationText, transcriptRows, snippetParts, takenBack, liveEntries,
    cacheState, cacheLabel, nextCall, cacheHit };
}
