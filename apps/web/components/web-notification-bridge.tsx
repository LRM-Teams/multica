"use client";

import { useCallback, useEffect, useRef } from "react";
import {
  registerSystemNotificationClickHandler,
  type SystemNotificationPayload,
} from "@multica/core/platform";
import { paths } from "@multica/core/paths";
import { useNavigation } from "@multica/views/navigation";

/**
 * Routes browser notification clicks to the source workspace's inbox, focused
 * on the clicked item. The web counterpart of the desktop `DesktopInboxBridge`:
 * desktop receives the click via Electron IPC, web wires it through the
 * Notification API's `onclick` (registered here into the core singleton).
 *
 * The route uses the `slug` the notification was emitted with — the SOURCE
 * workspace — not the active one, so a click always opens the right inbox even
 * after the user switches workspaces (#3766). An empty slug (unresolved
 * source) is ignored. Marking the row read is handled by InboxPage's
 * selected-item effect, which covers the `?issue=` URL-param path.
 */
export function WebNotificationBridge() {
  const { push } = useNavigation();
  // The adapter identity changes with the current route; the ref keeps the
  // registered click handler stable while always calling the latest push.
  const pushRef = useRef(push);
  useEffect(() => {
    pushRef.current = push;
  }, [push]);

  const buildNotificationPath = useCallback(
    ({ slug, issueKey, channelId, dmId }: SystemNotificationPayload) => {
      if (!slug) return null;
      const wsPaths = paths.workspace(slug);
      if (dmId) return `${wsPaths.channels()}?dm=${encodeURIComponent(dmId)}`;
      if (channelId) return `${wsPaths.channels()}?channel=${encodeURIComponent(channelId)}`;
      const selector = issueKey ? `?issue=${encodeURIComponent(issueKey)}` : "";
      return `${wsPaths.inbox()}${selector}`;
    },
    [],
  );

  useEffect(() => {
    registerSystemNotificationClickHandler((payload) => {
      const target = buildNotificationPath(payload);
      if (target) pushRef.current(target);
    });
    return () => registerSystemNotificationClickHandler(null);
  }, [buildNotificationPath]);

  useEffect(() => {
    if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
    void navigator.serviceWorker.register("/sw.js").catch(() => {
      // Notifications degrade to in-app unread state when SW registration fails.
    });
  }, []);

  useEffect(() => {
    if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
    const handleMessage = (event: MessageEvent) => {
      const data = event.data as { type?: string; payload?: SystemNotificationPayload } | null;
      if (data?.type !== "multica:notification-click" || !data.payload) return;
      const target = buildNotificationPath(data.payload);
      if (target) pushRef.current(target);
    };
    navigator.serviceWorker.addEventListener("message", handleMessage);
    return () => navigator.serviceWorker.removeEventListener("message", handleMessage);
  }, [buildNotificationPath]);

  return null;
}
