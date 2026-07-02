import { api } from "../api";
import type { WebPushSubscriptionPayload } from "../types";

export type WebPushSupportState =
  | "supported"
  | "unsupported"
  | "ios_requires_pwa"
  | "permission_denied";

const SERVICE_WORKER_PATH = "/web-push-sw.js";

type PushSubscriptionJSONValue = {
  endpoint?: string;
  expirationTime?: number | null;
  keys?: { p256dh?: string; auth?: string };
};

function browserWindow(): Window | null {
  return typeof window === "undefined" ? null : window;
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
  if (!("serviceWorker" in win.navigator) || !("PushManager" in win) || !("Notification" in win)) {
    return "unsupported";
  }
  if (win.Notification.permission === "denied") return "permission_denied";
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
  const permission = await win.Notification.requestPermission();
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
  const existing = await registration.pushManager.getSubscription();
  if (existing) return existing;
  return registration.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: urlBase64ToUint8Array(publicKey),
  });
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

function urlBase64ToUint8Array(value: string): Uint8Array {
  const padding = "=".repeat((4 - (value.length % 4)) % 4);
  const base64 = (value + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const output = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i += 1) output[i] = raw.charCodeAt(i);
  return output;
}
