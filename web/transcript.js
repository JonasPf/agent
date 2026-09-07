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
    parts.push({ text: m[0], id: m[0] });
    at = m.index + m[0].length;
  }
  if (at < s.length) parts.push({ text: s.slice(at) });
  return parts;
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { toolResult, resultBody, resultPreview, callBody, callPreview,
    eventLabel, eventDetail, eventTime, isFailure,
    speaker, bubbleClass, messageTime, runTime, lateBy, jobCost, splitSessionIds };
}
