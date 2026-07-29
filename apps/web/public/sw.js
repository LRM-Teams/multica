// Single service worker for the web app (LRM-679): notification click routing
// AND Web Push must live in one file. /sw.js and /web-push-sw.js were both
// registered on scope "/" — service worker registrations are keyed by scope,
// so the two register() calls kept replacing each other's script. Whenever
// the click-only script won, incoming push events had no `push` listener and
// were dropped silently: the server saw 201 delivered while no banner showed
// ("foreground works, background doesn't").

self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    data = {};
  }
  const title = data.title || "Multica";
  // Slack-like: same conversation replaces prior banner (channel_id / issue_key).
  // Fall back to item_id so unrelated pushes still show separately.
  const tag = data.channel_id || data.issue_key || data.item_id || undefined;
  const options = {
    body: data.body || "",
    tag,
    renotify: Boolean(tag),
    data: {
      url: data.url || "",
      slug: data.slug || "",
      issueKey: data.issue_key || "",
      itemId: data.item_id || "",
      channelId: data.channel_id || "",
    },
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const payload = event.notification.data || {};
  const targetUrl = payload.url || "/";
  event.waitUntil((async () => {
    const allClients = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    for (const client of allClients) {
      client.postMessage({ type: "multica:notification-click", payload });
      if ("focus" in client) return client.focus();
    }
    if (self.clients.openWindow) return self.clients.openWindow(targetUrl);
  })());
});
