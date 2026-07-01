"use client";

export type BrowserNotificationCapability =
  | "unsupported"
  | "standard"
  | "ios-pwa"
  | "ios-needs-pwa";

function getNavigator(): Navigator | null {
  return typeof navigator === "undefined" ? null : navigator;
}

function getWindow(): Window | null {
  return typeof window === "undefined" ? null : window;
}

export function isIOSBrowser(): boolean {
  const nav = getNavigator();
  if (!nav) return false;
  const ua = nav.userAgent || "";
  const platform = nav.platform || "";
  return /iPad|iPhone|iPod/.test(ua) || (platform === "MacIntel" && nav.maxTouchPoints > 1);
}

export function isStandaloneDisplayMode(): boolean {
  const win = getWindow();
  const nav = getNavigator();
  if (!win || !nav) return false;
  const standaloneNavigator = nav as Navigator & { standalone?: boolean };
  return win.matchMedia?.("(display-mode: standalone)").matches === true || standaloneNavigator.standalone === true;
}

export function isPushNotificationSupported(): boolean {
  const nav = getNavigator();
  const win = getWindow();
  return !!nav && !!win && "serviceWorker" in nav && "PushManager" in win && "Notification" in win;
}

/**
 * Mobile browser support split:
 * - Android Chrome/Edge can request notifications in-browser.
 * - iOS requires the site to run as a Home Screen web app before push/permission is useful.
 */
export function getBrowserNotificationCapability(): BrowserNotificationCapability {
  if (!isPushNotificationSupported()) return "unsupported";
  if (!isIOSBrowser()) return "standard";
  return isStandaloneDisplayMode() ? "ios-pwa" : "ios-needs-pwa";
}
