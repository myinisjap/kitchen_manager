// Bump CACHE whenever the SHELL list or caching logic changes.
const CACHE = 'km-v2';
// Relative entries resolve against this script's URL, so they work under any prefix.
const SHELL = ['./', 'index.html', 'app.js', 'js/api.js', 'js/inventory.js', 'js/scanner.js', 'js/shopping.js', 'js/recipes.js', 'js/calendar.js', 'js/meals.js', 'theme-init.js', 'tailwind.css', 'vendor/alpine-3.14.9.min.js', 'vendor/zxing-0.19.1.min.js', 'manifest.json', 'icons/icon-192.png', 'icons/icon-512.png'];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith('km-') && k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('message', (event) => {
  if (event.data === 'clear') event.waitUntil(caches.delete(CACHE));
});

function cacheable(res) {
  return res && res.ok && !res.redirected && res.type === 'basic';
}

async function networkFirst(request, cacheKey, fallback) {
  try {
    const res = await fetch(request);
    if (cacheable(res)) {
      const cache = await caches.open(CACHE);
      await cache.put(cacheKey, res.clone());
    }
    return res;
  } catch (err) {
    const cache = await caches.open(CACHE);
    const hit = await cache.match(cacheKey);
    if (hit) return hit;
    if (fallback) return fallback();
    throw err;
  }
}

self.addEventListener('fetch', (event) => {
  const request = event.request;
  if (request.method !== 'GET') return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  const scopePath = new URL(self.registration.scope).pathname;
  if (!url.pathname.startsWith(scopePath)) return;
  const rel = url.pathname.slice(scopePath.length);

  if (request.mode === 'navigate') {
    if (rel !== '' && rel !== 'index.html') return;
    event.respondWith(networkFirst(request, new URL('./', self.registration.scope).href));
    return;
  }
  if (rel.startsWith('api/shopping/')) {
    event.respondWith(networkFirst(request, request, () =>
      new Response('[]', { status: 503, headers: { 'Content-Type': 'application/json' } })));
    return;
  }
  if (rel.startsWith('api/') || rel.startsWith('auth/') || rel === 'ws' || rel === 'healthz') return;
  const inShell = SHELL.some((s) => new URL(s, self.location.href).href === url.origin + url.pathname);
  if (inShell) event.respondWith(networkFirst(request, request));
});
