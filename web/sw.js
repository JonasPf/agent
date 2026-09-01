self.addEventListener('install', e => self.skipWaiting());
self.addEventListener('activate', e => e.waitUntil(self.clients.claim()));

self.addEventListener('push', event => {
  let n = {};
  try { n = event.data.json(); } catch (e) { n = { title: 'agent', body: event.data ? event.data.text() : '' }; }
  event.waitUntil(self.registration.showNotification(n.title || 'agent', {
    body: n.body || '',
    tag: n.job_id || n.session_id || 'agent',
    data: { session_id: n.session_id || '' },
    requireInteraction: false
  }));
});

self.addEventListener('notificationclick', event => {
  event.notification.close();
  const id = event.notification.data && event.notification.data.session_id;
  const url = id ? '/#session/' + id : '/';
  event.waitUntil(clients.matchAll({ type: 'window', includeUncontrolled: true }).then(list => {
    for (const c of list) { if ('focus' in c) { c.navigate(url); return c.focus(); } }
    return clients.openWindow(url);
  }));
});
