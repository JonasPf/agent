'use strict';

// Notifying about a new message. This is the interface telling the operator
// about output they have not read; the agent has no say in it and no tool for
// it (ADR-026, ADR-027). It works only while a tab is open — there is no push,
// no service worker, and nothing that reaches a closed browser.

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

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { shouldNotify, messageNotification, canShowSystemNotification };
}
