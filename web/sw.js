const CACHE_NAME = 'gitgost-v8';
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

  // Document (HTML) requests are served network-first so the app shell stays
  // fresh; everything else is cache-first for performance.
  const isDocumentPath = url.pathname === '/' || url.pathname.endsWith('.html') ||
    /^\/(?:gh|gl|cb)\//.test(url.pathname);

  // API requests are dynamic JSON endpoints: treat them as network-first but
  // never as HTML documents, so an HTML error/challenge page is never cached
  // under an API URL (which would later break JSON parsing).
  const isApiPath = url.pathname.startsWith('/api/') ||
    url.pathname.startsWith('/v1/') || url.pathname.startsWith('/v2/');

  const networkFirst = isDocumentPath || isApiPath;

  // Only document navigations are expected to carry an HTML body. API and
  // asset responses that come back as HTML are error/challenge pages and must
  // not be cached.
  const expectHtmlBody = request.destination === 'document' ||
    request.destination === 'iframe' || request.destination === 'fencedframe' ||
    (isDocumentPath && url.pathname !== '/sw.js');

  function isHtmlContent(response) {
    const type = (response.headers.get('content-type') || '').toLowerCase();
    return type.startsWith('text/html') || type.startsWith('application/xhtml');
  }

  function cacheable(response) {
    return expectHtmlBody || !isHtmlContent(response);
  }

  if (networkFirst) {
    event.respondWith(
      fetch(request)
        .then((response) => {
          if (response.ok && !response.redirected && cacheable(response)) {
            const clone = response.clone();
            caches.open(CACHE_NAME).then((cache) => cache.put(request, clone));
          } else if (response.ok === false || isHtmlContent(response)) {
            // Drop any previously cached HTML/error body for this URL.
            caches.open(CACHE_NAME).then((cache) => cache.delete(request));
          }
          return response;
        })
        .catch(() => caches.match(request).then((response) => {
          if (response && cacheable(response)) return response;
          return caches.match('/index.html');
        }))
    );
    return;
  }

  event.respondWith(
    caches.match(request).then((cached) => {
      if (cached && cacheable(cached)) {
        return cached;
      }
      if (cached && !cacheable(cached)) {
        // Purge stale HTML/error bodies cached for non-document assets.
        caches.open(CACHE_NAME).then((cache) => cache.delete(request));
      }
      return fetch(request).then((response) => {
        if (response.ok && cacheable(response)) {
          const clone = response.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(request, clone));
        } else if (isHtmlContent(response)) {
          caches.open(CACHE_NAME).then((cache) => cache.delete(request));
        }
        return response;
      });
    })
  );
});
