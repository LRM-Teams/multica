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
