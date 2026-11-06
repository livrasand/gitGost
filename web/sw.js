const CACHE_NAME = 'gitgost-v7';
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

  if (url.origin !== self.location.origin || request.method !== 'GET') {
    return;
  }

  const isDocumentPath = url.pathname === '/' || url.pathname.endsWith('.html') ||
    url.pathname.startsWith('/api/') || /^\/(?:gh|gl|cb)\//.test(url.pathname);

  const expectHtmlBody = request.destination === 'document' ||
    request.destination === 'iframe' || request.destination === 'fencedframe' ||
    isDocumentPath || url.pathname === '/index.html';

  function isHtmlContent(response) {
    const type = (response.headers.get('content-type') || '').toLowerCase();
    return type.startsWith('text/html') || type.startsWith('application/xhtml');
  }

  function cacheable(response) {
    return expectHtmlBody || !isHtmlContent(response);
  }

  if (url.pathname === '/' || url.pathname.endsWith('.html') || url.pathname.startsWith('/api/') || /^\/(?:gh|gl|cb)\//.test(url.pathname)) {
    event.respondWith(
      fetch(request)
        .then((response) => {
          if (response.ok && !response.redirected && cacheable(response)) {
            const clone = response.clone();
            caches.open(CACHE_NAME).then((cache) => cache.put(request, clone));
          }
          return response;
        })
        .catch(() => caches.match(request).then((response) => response || caches.match('/index.html')))
    );
    return;
  }

  event.respondWith(
    caches.match(request).then((cached) => {
      if (cached && cacheable(cached)) {
        return cached;
      }
      return fetch(request).then((response) => {
        if (response.ok && cacheable(response)) {
          const clone = response.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(request, clone));
        }
        return response;
      });
    })
  );
});
