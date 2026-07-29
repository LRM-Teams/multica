"use client";

import { paths } from "../paths/paths";

// Native system notification bridge for the WEB app.
//
// The desktop app renders OS banners through its Electron main process
// (`window.desktopAPI.showNotification`); the web app has no such bridge, so it
// uses the browser Notification API here. `handleInboxNew` (realtime sync) is
// the single decision point — it already gates on focus + the source
// workspace's mute preference — and calls `showWebNotification` as the web path
// when no `desktopAPI` is present.
//
// Lives in `packages/core` (not `packages/views`) because the caller
// (`handleInboxNew`) lives in core and `views` cannot be imported from core
// (dependency direction is views → core). The click-routing decision, which
// needs the app router, is injected by the host app via
// `registerSystemNotificationClickHandler` instead — core stays headless.

export interface SystemNotificationPayload {
  /**
   * Source workspace slug. Empty when it couldn't be resolved — the click is
   * then a no-op rather than routing to the wrong workspace (#3766).
   */
  slug: string;
  /** Web route to open when a ServiceWorker-rendered notification is clicked. */
  url?: string;
  /** Inbox row id — lets the click mark the item read. */
  itemId?: string;
  /** `?issue=<…>` selector for the inbox page (issue id, else the item id). */
  issueKey?: string;
  /** Channel row id for chat/channel notifications. */
  channelId?: string;
  /** Direct-message channel row id for DM notifications. */
  dmId?: string;
  /** Thread root message id when the notified message is a thread reply. */
  threadId?: string;
  title: string;
  body: string;
}

/**
 * Single source of truth for where a notification click lands (LRM-736).
 * Conversation banners deep-link into the channel/DM with `?message=<itemId>`
 * (plus `?thread=<threadId>` for replies) so the channels page pages back to,
 * scrolls to, and highlights the exact triggering message — the same deep-link
 * Reminder anchors already use. Inbox banners keep the `?issue=` selector.
 * Returns null when the source workspace slug is unknown: the click is then a
 * no-op rather than routing to the wrong workspace (#3766).
 */
export function buildNotificationRoute(
  payload: Pick<
    SystemNotificationPayload,
    "slug" | "issueKey" | "channelId" | "dmId" | "itemId" | "threadId"
  >,
): string | null {
  if (!payload.slug) return null;
  const wsPaths = paths.workspace(payload.slug);
  const conversationId = payload.dmId || payload.channelId;
  if (conversationId) {
    const params = new URLSearchParams();
    if (payload.threadId) params.set("thread", payload.threadId);
    if (payload.itemId) params.set("message", payload.itemId);
    const selector = params.toString();
    return `${wsPaths.channelDetail(conversationId)}${selector ? `?${selector}` : ""}`;
  }
  const selector = payload.issueKey ? `?issue=${encodeURIComponent(payload.issueKey)}` : "";
  return `${wsPaths.inbox()}${selector}`;
}

type ClickHandler = (payload: SystemNotificationPayload) => void;

// Module-level singleton — mirrors how the desktop preload registers its
// behavior once at boot. The web shell registers a router-aware handler; while
// unregistered (SSR, tests, pre-mount) a click is a silent no-op.
let clickHandler: ClickHandler | null = null;

/**
 * Register how a clicked web notification routes (focus + navigate to the
 * source workspace's inbox, focused on the item). Called once by the web app
 * shell; pass `null` to unregister. Desktop does NOT use this — it routes
 * through its own Electron IPC bridge (`onInboxOpen`).
 */
export function registerSystemNotificationClickHandler(
  handler: ClickHandler | null,
): void {
  clickHandler = handler;
}

// Read the Notification constructor off `window` (rather than the bare global)
// so it's both SSR-safe and injectable from the core Node test environment,
// where `window`/`Notification` don't exist by default.
function getNotificationCtor(): typeof Notification | null {
  if (typeof window === "undefined") return null;
  const ctor = (window as { Notification?: typeof Notification }).Notification;
  return typeof ctor === "function" ? ctor : null;
}

/** True when the browser exposes the Notification API (false on SSR / old engines). */
export function isWebNotificationSupported(): boolean {
  return getNotificationCtor() !== null;
}

export type WebNotificationPermission = NotificationPermission | "unsupported";

/** Current permission, or "unsupported" when the API is unavailable. */
export function getWebNotificationPermission(): WebNotificationPermission {
  const ctor = getNotificationCtor();
  return ctor ? ctor.permission : "unsupported";
}

/**
 * Prompt for notification permission. Resolves to the resulting permission, or
 * "unsupported". Only "default" triggers the browser prompt — an
 * already-decided permission (granted/denied) is returned as-is.
 */
export async function requestWebNotificationPermission(): Promise<WebNotificationPermission> {
  const ctor = getNotificationCtor();
  if (!ctor) return "unsupported";
  if (ctor.permission !== "default") return ctor.permission;
  try {
    return await ctor.requestPermission();
  } catch {
    // Older Safari used a callback signature and can reject the promise form.
    return ctor.permission;
  }
}

/**
 * Show a native browser notification. Chrome on Android requires notifications
 * to be displayed through a ServiceWorkerRegistration, while desktop browsers
 * usually allow `new Notification`. Try the page path first for click-handler
 * routing, then fall back to the service worker for mobile browsers.
 */
export function showWebNotification(payload: SystemNotificationPayload): void {
  const ctor = getNotificationCtor();
  if (!ctor || ctor.permission !== "granted") return;
  // Conversation banners keep the conversation-level tag so a new message
  // replaces the prior banner from the same channel/DM (Slack-like dedup);
  // itemId is only the fallback for inbox banners (LRM-736 added itemId to
  // conversation payloads for click deep-linking — it must not fragment tags).
  const tag = payload.dmId ?? payload.channelId ?? payload.itemId ?? payload.title;
  const options: NotificationOptions = {
    body: payload.body,
    tag,
    data: payload,
  };
  try {
    const notification = new ctor(payload.title, options);
    notification.onclick = () => {
      try {
        window.focus();
      } catch {
        // Best-effort; some browsers disallow programmatic focus.
      }
      notification.close();
      clickHandler?.(payload);
    };
    return;
  } catch {
    // Fall through to the service-worker path below.
  }

  const sw = typeof navigator !== "undefined" ? navigator.serviceWorker : undefined;
  if (!sw?.ready) return;
  void sw.ready
    .then((registration) => registration.showNotification(payload.title, options))
    .catch(() => {
      // The in-app inbox/unread state still surfaces the event.
    });
}
