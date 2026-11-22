'use strict';

// The service worker is here for two things. One is a push about a message
// the operator has not read (ADR-066), which arrives whether or not a page is
// open. It shows what the agent sent, as sent. Whether to push at all was
// decided on the server, so every push shows a notification: Safari withdraws
// a subscription whose pushes show nothing.
//
// The other is the app opened on a device with no connection at all, which
// gets a page of its own saying so rather than the browser's error
// (webui.html, "Installation").

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));

// The worker answers a page being opened only while the device says it has no
// connection. With one, the browser asks the agent itself, as it would with no
// worker: the password in front of the agent is asked for by the browser, and
// a worker passing a refusal on can leave it unasked, on a phone that forgets
// the password daily. Whatever an open page asks for is left alone too; its
// own screens say what failed.
self.addEventListener('fetch', event => {
  if (event.request.mode !== 'navigate' || self.navigator.onLine !== false) return;
  event.respondWith(new Response(OFFLINE, { headers: { 'content-type': 'text/html; charset=utf-8' } }));
});

// OFFLINE is that page, whole: nothing it needs can be fetched. It is drawn as
// the app draws a screen it cannot fill, and opens the app once the device is
// back online.
const OFFLINE = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#faf8f5" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#151310" media="(prefers-color-scheme: dark)">
<title>agent</title>
<style>
:root { color-scheme: light; --bg: #faf8f5; --fg: #1a1917; --fg-2: #4c483f; --accent: #a3542a; --on-accent: #ffffff; }
@media (prefers-color-scheme: dark) {
  :root { color-scheme: dark; --bg: #151310; --fg: #ece7dd; --fg-2: #c5bfb3; --accent: #e09a63; --on-accent: #1a1611; }
}
body { margin: 0; background: var(--bg); color: var(--fg); -webkit-font-smoothing: antialiased;
  font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, Roboto, sans-serif; }
main { max-width: 48ch; padding: 24px; padding-top: calc(max(env(safe-area-inset-top), 16px) + 16px); }
.brand { display: flex; align-items: center; gap: 8px; font-weight: 600; margin-bottom: 56px; }
.brand::before { content: ''; width: 8px; height: 8px; border-radius: 999px; background: var(--accent); }
h1 { font-size: 18px; letter-spacing: -.01em; margin: 0 0 8px; }
p { font-size: 14px; line-height: 1.5; color: var(--fg-2); margin: 0 0 16px; }
button { font: inherit; font-size: 14px; font-weight: 550; color: var(--on-accent); background: var(--accent);
  border: 0; border-radius: 999px; padding: 8px 16px; }
</style>
</head>
<body>
<main>
<div class="brand">agent</div>
<h1>This device is offline</h1>
<p>The agent is reached over the network, and this device has none right now. The app opens by itself once it is back.</p>
<button type="button" onclick="location.reload()">Try again</button>
</main>
<script>addEventListener('online', () => location.reload());</script>
</body>
</html>
`;

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
