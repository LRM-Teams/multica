import { api } from "../api";
import type { WebPushSubscriptionPayload } from "../types";

export type WebPushSupportState =
  | "supported"
  | "unsupported"
  | "ios_requires_pwa"
  | "permission_denied";

// One service worker per scope (LRM-679): the notification bridge registers
// "/sw.js" on the default "/" scope, so push must use the same script —
// a second file on the same scope evicts the push handler.
const SERVICE_WORKER_PATH = "/sw.js";

type PushSubscriptionJSONValue = {
  endpoint?: string;
  expirationTime?: number | null;
  keys?: { p256dh?: string; auth?: string };
};

type NotificationAPI = Pick<typeof Notification, "permission" | "requestPermission">;

type WebPushWindow = Window & {
  Notification?: NotificationAPI;
  PushManager?: unknown;
};

function browserWindow(): WebPushWindow | null {
  return typeof window === "undefined" ? null : (window as WebPushWindow);
}

function getNotificationAPI(win = browserWindow()): NotificationAPI | null {
  return win?.Notification ?? null;
}

function isStandaloneDisplay(): boolean {
  const win = browserWindow();
  if (!win) return false;
  const nav = win.navigator as Navigator & { standalone?: boolean };
  return Boolean(nav.standalone) || win.matchMedia?.("(display-mode: standalone)").matches === true;
}

function isIOS(): boolean {
  const win = browserWindow();
  if (!win) return false;
  const nav = win.navigator;
  return /iPad|iPhone|iPod/.test(nav.userAgent) || (nav.platform === "MacIntel" && nav.maxTouchPoints > 1);
}

export function getWebPushSupportState(): WebPushSupportState {
  const win = browserWindow();
  if (!win) return "unsupported";
  if (isIOS() && !isStandaloneDisplay()) return "ios_requires_pwa";
  const notification = getNotificationAPI(win);
  if (!("serviceWorker" in win.navigator) || !("PushManager" in win) || !notification) {
    return "unsupported";
  }
  if (notification.permission === "denied") return "permission_denied";
  return "supported";
}

export async function registerWebPushServiceWorker(): Promise<ServiceWorkerRegistration> {
  const win = browserWindow();
  if (!win || !("serviceWorker" in win.navigator)) {
    throw new Error("Service workers are not supported");
  }
  return win.navigator.serviceWorker.register(SERVICE_WORKER_PATH, { scope: "/" });
}

export async function requestAndBindWebPushSubscription(): Promise<WebPushSubscriptionPayload> {
  const win = browserWindow();
  if (!win || getWebPushSupportState() !== "supported") {
    throw new Error("Web Push is not supported on this device");
  }
  const notification = getNotificationAPI(win);
  if (!notification) {
    throw new Error("Notification permission API is not supported");
  }
  const permission = await notification.requestPermission();
  if (permission !== "granted") {
    throw new Error("Notification permission was not granted");
  }
  return bindCurrentWebPushSubscription();
}

export async function bindCurrentWebPushSubscription(): Promise<WebPushSubscriptionPayload> {
  const registration = await registerWebPushServiceWorker();
  const { public_key: publicKey, enabled } = await api.getWebPushPublicKey();
  if (!enabled || !publicKey) {
    throw new Error("Web Push is not configured on this server");
  }
  const subscription = await getOrCreateSubscription(registration, publicKey);
  const payload = subscriptionToPayload(subscription);
  await api.bindWebPushSubscription(payload);
  return payload;
}

export async function unbindCurrentWebPushSubscription(): Promise<void> {
  const win = browserWindow();
  if (!win || !("serviceWorker" in win.navigator)) return;
  const registration = await win.navigator.serviceWorker.getRegistration("/");
  const subscription = await registration?.pushManager.getSubscription();
  if (!subscription) return;
  await api.unbindWebPushSubscription(subscription.endpoint).catch(() => undefined);
  await subscription.unsubscribe().catch(() => false);
}

async function getOrCreateSubscription(registration: ServiceWorkerRegistration, publicKey: string): Promise<PushSubscription> {
  const applicationServerKey = urlBase64ToUint8Array(publicKey);
  const existing = await registration.pushManager.getSubscription();
  if (existing) {
    // LRM-679 缺陷 A (VAPID key rotation guard): if the existing
    // subscription was created with a different applicationServerKey, its
    // endpoint is now stale and will silently fail to receive pushes.
    // Unsubscribe and create a fresh subscription bound to the current key
    // so VAPID rotation (e.g. LRM-680) doesn't leave devices dark.
    if (hasMatchingApplicationServerKey(existing, applicationServerKey)) {
      return existing;
    }
    await existing.unsubscribe().catch(() => undefined);
  }
  return registration.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey,
  });
}

function hasMatchingApplicationServerKey(subscription: PushSubscription, expectedKey: Uint8Array): boolean {
  const actual = subscription.options?.applicationServerKey;
  if (!actual) return false;
  const actualBytes = toUint8Array(actual);
  if (actualBytes.length !== expectedKey.length) return false;
  for (let i = 0; i < actualBytes.length; i += 1) {
    if (actualBytes[i] !== expectedKey[i]) return false;
  }
  return true;
}

function toUint8Array(value: ArrayBuffer | ArrayBufferView): Uint8Array {
  if (value instanceof Uint8Array) return value;
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
}

function subscriptionToPayload(subscription: PushSubscription): WebPushSubscriptionPayload {
  const raw = subscription.toJSON() as PushSubscriptionJSONValue;
  const p256dh = raw.keys?.p256dh;
  const auth = raw.keys?.auth;
  if (!raw.endpoint || !p256dh || !auth) {
    throw new Error("PushSubscription is missing endpoint or keys");
  }
  return {
    endpoint: raw.endpoint,
    keys: { p256dh, auth },
    expiration_time: raw.expirationTime ?? null,
    device_id: raw.endpoint,
    user_agent: browserWindow()?.navigator.userAgent ?? "",
  };
}

function urlBase64ToUint8Array(value: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - (value.length % 4)) % 4);
  const base64 = (value + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const output = new Uint8Array(raw.length) as Uint8Array<ArrayBuffer>;
  for (let i = 0; i < raw.length; i += 1) output[i] = raw.charCodeAt(i);
  return output;
}
