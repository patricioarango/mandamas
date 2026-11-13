// App-shell cache. Bump VERSION on every deploy that changes these files.
const VERSION = "mandamas-v3";
const SHELL = [
  "./", "index.html", "manifest.webmanifest", "css/styles.css",
  "js/main.js", "js/ui.js", "js/firebase.js", "js/firebase-config.js",
  "js/views/login.js", "js/views/not-allowed.js", "js/views/home.js",
  "js/views/project.js", "js/views/issues.js", "js/views/editor.js", "js/views/project-form.js",
  "img/logo.png", "icons/icon-192.png", "icons/favicon-32.png",
];

self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(VERSION).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== VERSION).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);
  // Only same-origin GETs of app files. /api, Firebase and Google sign-in always go to the network.
  if (e.request.method !== "GET" || url.origin !== location.origin || url.pathname.startsWith("/api/")) return;
  // Network first so deploys show up right away; cache is the offline fallback.
  e.respondWith(
    fetch(e.request)
      .then((res) => {
        if (res.ok) { const copy = res.clone(); caches.open(VERSION).then((c) => c.put(e.request, copy)); }
        return res;
      })
      .catch(() => caches.match(e.request).then((r) => r || caches.match("index.html"))),
  );
});
