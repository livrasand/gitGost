const CACHE_NAME = 'gitgost-v2';
const PRECACHE_ASSETS = [
  '/',
  '/index.html',
  '/repo.html',
  '/assets/logos/android-icon-192x192.png',
  '/assets/logos/apple-icon-180x180.png',
  '/assets/logos/favicon.ico',
  '/assets/logos/gitgost-logo.svg',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll(PRECACHE_ASSETS))
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((names) =>
      Promise.all(
        names
          .filter((name) => name !== CACHE_NAME)
          .map((name) => caches.delete(name))
      )
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  const { request } = event;
  const url = new URL(request.url);

  // Skip cross-origin requests and non-GET requests
  if (url.origin !== self.location.origin || request.method !== 'GET') {
    return;
  }

  // Network-first for HTML pages, API calls and SPA profile routes (/gh, /gl, /cb).
  // Sin esto, las rutas de perfil (que no terminan en .html) caen en cache-first
  // y el navegador sirve profile.html viejo para siempre tras la primera visita.
  if (url.pathname === '/' || url.pathname.endsWith('.html') || url.pathname.startsWith('/api/') || /^\/(?:gh|gl|cb)\//.test(url.pathname)) {
    event.respondWith(
      fetch(request)
        .then((response) => {
          if (response.ok) {
            const clone = response.clone();
            caches.open(CACHE_NAME).then((cache) => cache.put(request, clone));
          }
          return response;
        })
        .catch(() => caches.match(request).then((response) => response || caches.match('/index.html')))
    );
    return;
  }

  // Cache-first for static assets
  event.respondWith(
    caches.match(request).then((cached) => {
      if (cached) {
        return cached;
      }
      return fetch(request).then((response) => {
        if (response.ok) {
          const clone = response.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(request, clone));
        }
        return response;
      });
    })
  );
});
