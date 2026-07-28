self.addEventListener('push', (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    data = {};
  }
  const title = data.title || 'Multica';
  // Slack-like: same conversation replaces prior banner (channel_id / issue_key).
  // Fall back to item_id so unrelated pushes still show separately.
  const tag = data.channel_id || data.issue_key || data.item_id || undefined;
  const options = {
    body: data.body || '',
    tag,
    renotify: Boolean(tag),
    data: {
      url: data.url || '',
      slug: data.slug || '',
      issueKey: data.issue_key || '',
      itemId: data.item_id || '',
      channelId: data.channel_id || '',
    },
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const targetUrl = event.notification.data && event.notification.data.url;
  if (!targetUrl) return;
  event.waitUntil((async () => {
    const clientsList = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const client of clientsList) {
      if ('focus' in client) {
        client.navigate(targetUrl);
        return client.focus();
      }
    }
    if (self.clients.openWindow) return self.clients.openWindow(targetUrl);
  })());
});

// LRM-679 缺陷 B: handle pushsubscriptionchange so a rotated/invalidated
// subscription is re-created automatically. Safari's support is spotty, so
// the page-side key-mismatch guard (getOrCreateSubscription, 缺陷 A) is the
// authoritative path; this covers Chrome and any browser that fires the event.
self.addEventListener('pushsubscriptionchange', (event) => {
  event.waitUntil((async () => {
    try {
      const res = await self.fetch('/api/web-push/public-key', { credentials: 'include' });
      if (!res.ok) return;
      const { enabled, public_key: publicKey } = await res.json();
      if (!enabled || !publicKey) return;
      const padding = '='.repeat((4 - (publicKey.length % 4)) % 4);
      const base64 = (publicKey + padding).replace(/-/g, '+').replace(/_/g, '/');
      const raw = self.atob(base64);
      const key = Uint8Array.from(raw, (c) => c.charCodeAt(0));
      const newSubscription = await self.registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: key,
      });
      const payload = newSubscription.toJSON();
      // Best-effort rebind. SW cannot read the CSRF cookie to set X-CSRF-Token,
      // so this may 403 on CSRF-strict servers; the page-side guard
      // (bindCurrentWebPushSubscription) will complete the rebind on next open.
      await self.fetch('/api/web-push/subscriptions', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          endpoint: payload.endpoint,
          keys: { p256dh: payload.keys?.p256dh, auth: payload.keys?.auth },
          expiration_time: payload.expirationTime ?? null,
          device_id: payload.endpoint,
          user_agent: self.navigator.userAgent,
        }),
      }).catch(() => undefined);
    } catch {
      /* page-side guard will retry on next open */
    }
  })());
});
