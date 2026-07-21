"use client";

/**
 * Soft-prompt dismissal for the WeChat-style browser notification dialog.
 * Lives in core so tests can drive the same storage key the UI uses.
 */

const STORAGE_KEY = "multica.browserNotificationPrompt";

export type BrowserNotificationPromptDecision =
  | { status: "undecided" }
  | { status: "dismissed"; at: number }
  | { status: "snoozed"; until: number };

type StorageLike = {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem?(key: string): void;
};

function getStorage(): StorageLike | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function readBrowserNotificationPromptDecision(
  now = Date.now(),
  storage: StorageLike | null = getStorage(),
): BrowserNotificationPromptDecision {
  if (!storage) return { status: "undecided" };
  try {
    const raw = storage.getItem(STORAGE_KEY);
    if (!raw) return { status: "undecided" };
    const parsed = JSON.parse(raw) as {
      status?: string;
      at?: number;
      until?: number;
    };
    if (parsed.status === "dismissed" && typeof parsed.at === "number") {
      return { status: "dismissed", at: parsed.at };
    }
    if (parsed.status === "snoozed" && typeof parsed.until === "number") {
      if (parsed.until > now) return { status: "snoozed", until: parsed.until };
      return { status: "undecided" };
    }
  } catch {
    // Corrupt storage — treat as undecided.
  }
  return { status: "undecided" };
}

export function shouldShowBrowserNotificationPrompt(
  permission: NotificationPermission | "unsupported" | "default" | "granted" | "denied",
  capability: "unsupported" | "ios-needs-pwa" | "ios-pwa" | "standard",
  now = Date.now(),
  storage: StorageLike | null = getStorage(),
): boolean {
  if (permission !== "default") return false;
  if (capability !== "standard" && capability !== "ios-pwa") return false;
  return readBrowserNotificationPromptDecision(now, storage).status === "undecided";
}

export function dismissBrowserNotificationPrompt(
  storage: StorageLike | null = getStorage(),
  now = Date.now(),
): void {
  storage?.setItem(
    STORAGE_KEY,
    JSON.stringify({ status: "dismissed", at: now }),
  );
}

/** Snooze for 3 days — "稍后再说" / Later. */
export function snoozeBrowserNotificationPrompt(
  storage: StorageLike | null = getStorage(),
  now = Date.now(),
  snoozeMs = 3 * 24 * 60 * 60 * 1000,
): void {
  storage?.setItem(
    STORAGE_KEY,
    JSON.stringify({ status: "snoozed", until: now + snoozeMs }),
  );
}

export function clearBrowserNotificationPromptDecision(
  storage: StorageLike | null = getStorage(),
): void {
  storage?.removeItem?.(STORAGE_KEY);
}

export const BROWSER_NOTIFICATION_PROMPT_STORAGE_KEY = STORAGE_KEY;
