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
  // Absolute icon URL — relative paths are unreliable for push-delivered
  // notifications on some Chrome/OS combos, and a missing icon makes the
  // toast easy to miss ("received in tray, no banner") (LRM-679).
  const origin = self.location && self.location.origin ? self.location.origin : "";
  // Prefer PNG: WebKit/iOS push banners drop or hide SVG icons (LRM-684).
  const icon = data.icon || (origin ? `${origin}/icon-192.png` : "/icon-192.png");
  const options = {
    body: data.body || "",
    tag,
    renotify: Boolean(tag),
    icon,
    badge: data.badge || icon,
    // Test pushes set this so the banner stays until dismissed — easier for
    // Frank/self-test to confirm the OS toast path without racing tray collapse.
    requireInteraction: Boolean(data.require_interaction),
    timestamp: typeof data.timestamp === "number" ? data.timestamp : Date.now(),
    data: {
      url: data.url || "",
      slug: data.slug || "",
      issueKey: data.issue_key || "",
      itemId: data.item_id || "",
      channelId: data.channel_id || "",
      threadId: data.thread_id || "",
    },
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

// LRM-736: the no-open-window fallback must land on the triggering MESSAGE,
// not the bare channel — mirror the in-app deep link (?thread=/‌?message=).
function buildClickUrl(payload) {
  const url = payload.url || "/";
  // Client-emitted payloads already carry the anchor in `url`; only push-
  // payload URLs (bare channel route) need it appended here.
  if (!payload.itemId || !url.includes("/channels/") || url.includes("message=")) return url;
  const params = payload.threadId
    ? `thread=${encodeURIComponent(payload.threadId)}&message=${encodeURIComponent(payload.itemId)}`
    : `message=${encodeURIComponent(payload.itemId)}`;
  return `${url}${url.includes("?") ? "&" : "?"}${params}`;
}

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const payload = event.notification.data || {};
  event.waitUntil((async () => {
    const allClients = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    // Route the click into exactly ONE window: the one we focus. Messaging
    // every client made all open tabs navigate away from their own context.
    const target = allClients.find((client) => "focus" in client);
    if (target) {
      target.postMessage({ type: "multica:notification-click", payload });
      return target.focus();
    }
    if (self.clients.openWindow) return self.clients.openWindow(buildClickUrl(payload));
  })());
});
