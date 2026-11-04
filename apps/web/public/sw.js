// sous service worker: keeps the app shell available offline and shows
// Web Push notifications. The API and the event stream are never cached.
const CACHE = 'sous-shell-v3'
const SHELL = ['/', '/manifest.webmanifest', '/icon.svg', '/theme-init.js']

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()))
})

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url)
  if (e.request.method !== 'GET' || url.origin !== location.origin) return
  if (url.pathname.startsWith('/api/') || url.pathname === '/ws' || url.pathname === '/healthz') return

  if (e.request.mode === 'navigate') {
    // Network first, so a new deploy shows up; fall back to the cached shell.
    e.respondWith(
      fetch(e.request)
        .then((res) => {
          const copy = res.clone()
          caches.open(CACHE).then((c) => c.put('/', copy))
          return res
        })
        .catch(() => caches.match('/')),
    )
    return
  }
  // Hashed assets never change: cache first.
  e.respondWith(
    caches.match(e.request).then(
      (hit) =>
        hit ||
        fetch(e.request).then((res) => {
          if (res.ok && url.pathname.startsWith('/assets/')) {
            const copy = res.clone()
            caches.open(CACHE).then((c) => c.put(e.request, copy))
          }
          return res
        }),
    ),
  )
})

// A push from the Hub: {title, body, url, tag, kind}.
self.addEventListener('push', (e) => {
  let m = { title: 'sous', body: '' }
  try {
    m = e.data ? e.data.json() : m
  } catch {
    m.body = e.data ? e.data.text() : ''
  }
  e.waitUntil(
    self.registration.showNotification(m.title || 'sous', {
      body: m.body || '',
      tag: m.tag || undefined,
      icon: '/icon.svg',
      badge: '/icon.svg',
      data: { url: m.url || '/' },
      requireInteraction: m.kind === 'approval',
    }),
  )
})

// Clicking a notification opens the page it points at: an open window is
// focused and told where to go; otherwise a new one opens there.
self.addEventListener('notificationclick', (e) => {
  e.notification.close()
  const url = new URL((e.notification.data && e.notification.data.url) || '/', self.location.origin).href
  e.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((list) => {
      for (const c of list) {
        if (new URL(c.url).origin === self.location.origin) {
          c.postMessage({ type: 'open', url })
          return c.focus()
        }
      }
      return self.clients.openWindow(url)
    }),
  )
})
