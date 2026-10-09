// Service Worker: zeigt Push-Nachrichten und öffnet beim Antippen den Termin.
// Er cacht bewusst nichts, damit keine personenbezogenen Daten auf dem Gerät liegen bleiben.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));

self.addEventListener('push', (e) => {
  let d = {};
  try {
    d = e.data ? e.data.json() : {};
  } catch (_) {
    d = { text: e.data.text() };
  }
  e.waitUntil(self.registration.showNotification(d.titel || 'Teamtafel', {
    body: d.text || '',
    icon: '/static/icon-192.png',
    badge: '/static/icon-192.png',
    lang: 'de',
    data: { url: d.url || '/' },
  }));
});

self.addEventListener('notificationclick', (e) => {
  e.notification.close();
  const url = (e.notification.data && e.notification.data.url) || '/';
  e.waitUntil((async () => {
    const fenster = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const f of fenster) {
      if (f.url === url && 'focus' in f) return f.focus();
    }
    return self.clients.openWindow(url);
  })());
});
