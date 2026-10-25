'use strict';

// Notifying about a new message. This is the interface telling the operator
// about output they have not read; the agent has no say in it and no tool for
// it (ADR-026). A device that can be pushed to is told by the agent, through
// the service worker, even with the browser closed (ADR-066); one that cannot
// gets a banner from the page while a tab is open (ADR-027).

// shouldNotify decides whether an arriving transcript entry is worth a banner.
// Only what the agent said counts, and only when the operator is not already
// looking at the conversation it landed in.
function shouldNotify(entry, sessionID, ctx) {
  if (!entry || entry.type !== 'message' || entry.role !== 'assistant') return false;
  if (!entry.text) return false; // a turn that only called tools has said nothing yet
  const c = ctx || {};
  const watching = c.view === 'session' && c.arg === sessionID && c.visible;
  return !watching;
}

// messageNotification is what the operating system shows: the conversation as
// the title, its newest line as the body. It is a pointer to the transcript,
// not a copy of it, so the body is one clipped line.
function messageNotification(entry, sessionID, sessionTitle) {
  const first = String((entry && entry.text) || '').split('\n')[0].trim();
  return {
    title: sessionTitle || 'agent',
    body: first.length > 140 ? first.slice(0, 139) + '…' : first,
    // One banner per conversation: a later message replaces the one before it
    // rather than stacking a queue the operator has to dismiss.
    tag: 'agent:' + sessionID,
    sessionID: sessionID
  };
}

// canShowSystemNotification decides whether the page may hand this to the
// operating system. When it may not, the unread count in the session list is
// the whole of the signal — the interface never opens a popup of its own.
function canShowSystemNotification(env) {
  return !!(env && env.hasNotification && env.permission === 'granted');
}

// pushSupport says what this browser can do with a push. An iPhone or iPad
// has the API only in the home-screen app, and is the one browser that has
// navigator.standalone at all, so false there means "not from here".
function pushSupport(env) {
  const e = env || {};
  if (e.hasServiceWorker && e.hasPushManager && e.hasNotification) return 'push';
  if (e.standalone === false) return 'home-screen';
  return 'none';
}

// keyBytes turns the base64url key the agent serves into the bytes
// PushManager.subscribe takes.
function keyBytes(b64) {
  const s = String(b64 || '').replace(/-/g, '+').replace(/_/g, '/');
  const bin = atob(s + '='.repeat((4 - s.length % 4) % 4));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// sameKey says whether a subscription was made under the key the agent holds
// now. A push service refuses pushes signed with any other, so a subscription
// made under an earlier key reaches nothing however often it is registered.
function sameKey(subscribedWith, b64) {
  if (!subscribedWith) return false;
  const a = new Uint8Array(subscribedWith), b = keyBytes(b64);
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

// Whose push service a device uses, which is the nearest thing to its name the
// agent is told: the endpoint's host, and the browser behind it.
const PUSH_SERVICES = {
  'fcm.googleapis.com': 'Chrome',
  'web.push.apple.com': 'Safari',
  'updates.push.services.mozilla.com': 'Firefox'
};

// deviceLine is a registered device as the settings screen lists it: whose
// push service, and what it last answered. A refusal is the line that matters,
// since a push that fails otherwise looks exactly like a quiet agent.
function deviceLine(d, isThis) {
  const svc = (d && d.service) || '';
  const browser = PUSH_SERVICES[svc] || (/\.notify\.windows\.com$/.test(svc) ? 'Edge' : '');
  const who = isThis ? 'This device' + (browser ? ' · ' + browser : '') : browser || 'Another device';
  let said = 'Nothing pushed yet.';
  if (d && d.last_error) said = svc + ' refused the last push: ' + d.last_error;
  else if (d && d.last_status) said = svc + ' accepted the last push.';
  return { who, said, bad: !!(d && d.last_error) };
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { shouldNotify, messageNotification, canShowSystemNotification,
    pushSupport, keyBytes, sameKey, deviceLine };
}
