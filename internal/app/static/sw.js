// A pass-through service worker. Its only job is to exist: browsers require a
// registered service worker before they offer to install the app. Caching is
// out of scope, so every fetch simply goes to the network.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
self.addEventListener('fetch', () => {
  // No respondWith: the request falls through to the network untouched.
});
