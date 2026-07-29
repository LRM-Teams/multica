"use client";

import { useEffect, useRef } from "react";
import {
  buildNotificationRoute,
  registerSystemNotificationClickHandler,
  type SystemNotificationPayload,
} from "@multica/core/platform";
import { useNavigation } from "@multica/views/navigation";

/**
 * Routes browser notification clicks to the source workspace's inbox or
 * conversation, focused on the clicked item. The web counterpart of the
 * desktop `DesktopInboxBridge`: desktop receives the click via Electron IPC,
 * web wires it through the Notification API's `onclick` (registered here into
 * the core singleton).
 *
 * The route uses the `slug` the notification was emitted with — the SOURCE
 * workspace — not the active one, so a click always opens the right inbox even
 * after the user switches workspaces (#3766). An empty slug (unresolved
 * source) is ignored. Conversation banners deep-link to the triggering
 * message via `?message=` / `?thread=` (LRM-736); marking the row read is
 * handled by InboxPage's selected-item effect, which covers the `?issue=`
 * URL-param path.
 */
export function WebNotificationBridge() {
  const { push } = useNavigation();
  // The adapter identity changes with the current route; the ref keeps the
  // registered click handler stable while always calling the latest push.
  const pushRef = useRef(push);
  useEffect(() => {
    pushRef.current = push;
  }, [push]);

  useEffect(() => {
    registerSystemNotificationClickHandler((payload) => {
      const target = buildNotificationRoute(payload);
      if (target) pushRef.current(target);
    });
    return () => registerSystemNotificationClickHandler(null);
  }, []);

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
      const target = buildNotificationRoute(data.payload);
      if (target) pushRef.current(target);
    };
    navigator.serviceWorker.addEventListener("message", handleMessage);
    return () => navigator.serviceWorker.removeEventListener("message", handleMessage);
  }, []);

  return null;
}
