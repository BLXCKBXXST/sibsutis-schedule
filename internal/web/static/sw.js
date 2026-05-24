// Service Worker для оффлайн-просмотра расписания.
//
// Стратегия — «network-first, cache fallback»:
// 1. Пытаемся сходить за свежей версией страницы.
// 2. При успехе кладём ответ в CacheStorage и возвращаем клиенту.
// 3. При сетевой ошибке (или сервер вернул 5xx) отдаём то, что лежит в
//    кэше. Это покрывает обычный кейс «увидел расписание дома, утром
//    в метро — нет интернета, нужно глянуть».
//
// API-эндпоинты (/api/...), .ics, /healthz и формы (POST) кэширование
// игнорируют — для них нет смысла, и они могут вводить пользователя в
// заблуждение устаревшими данными.

const CACHE = 'sibsutis-v1';

// App-shell — то, что нужно для первого рендера; кладём в кэш при install.
const SHELL = [
  '/',
  '/static/app.css',
  '/static/app.js',
  '/static/manifest.json',
  '/static/icon.svg',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE).then((c) => c.addAll(SHELL)).catch(() => null)
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  // Удаляем чужие версии кэша при апдейте sw.
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  // На своём origin'е кэшируем; cross-origin (CDN, чужие API) — не трогаем.
  if (url.origin !== self.location.origin) return;

  // Не кэшируем «динамику», где устаревший ответ хуже отсутствия:
  if (
    url.pathname.startsWith('/api/') ||
    url.pathname.startsWith('/calendar/') ||
    url.pathname.startsWith('/ics/') ||
    url.pathname === '/healthz'
  ) {
    return;
  }

  event.respondWith(
    fetch(req)
      .then((resp) => {
        // Кэшируем только успешные ответы.
        if (resp && resp.status === 200) {
          const copy = resp.clone();
          caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => null);
        }
        return resp;
      })
      .catch(() =>
        caches.match(req).then((cached) =>
          cached ||
          new Response('Offline', {
            status: 503,
            statusText: 'Offline',
            headers: { 'Content-Type': 'text/plain; charset=utf-8' },
          })
        )
      )
  );
});
