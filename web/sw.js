'use strict';

// The service worker is here for one thing: a push about a message the
// operator has not read (ADR-066), which arrives whether or not a page is
// open. It shows what the agent sent, as sent. Whether to push at all was
// decided on the server, so every push shows a notification: Safari withdraws
// a subscription whose pushes show nothing.

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));

self.addEventListener('push', event => {
  let n = {};
  try { n = event.data ? event.data.json() : {}; } catch (e) {}
  event.waitUntil(self.registration.showNotification(n.title || 'agent', {
    body: n.body || '',
    tag: n.tag || 'agent',
    icon: '/icon.png',
    data: { session_id: n.session_id || '' }
  }));
});

// Opening a notification goes to its session, in a window already open if
// there is one. That window is told where to go rather than reloaded: a reload
// behind a lapsed password would ask for it again.
self.addEventListener('notificationclick', event => {
  event.notification.close();
  const id = (event.notification.data || {}).session_id || '';
  event.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(list => {
    const open = list.find(c => 'focus' in c);
    if (open) {
      open.postMessage({ open: id });
      return open.focus();
    }
    return self.clients.openWindow(id ? '/#session/' + id : '/');
  }));
});
