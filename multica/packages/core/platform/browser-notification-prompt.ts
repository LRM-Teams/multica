"use client";

/**
 * Soft-prompt dismissal for the Slack-style browser notification banner.
 * Lives in core so tests can drive the same storage key the UI uses.
 *
 * - "以后再说" / Later → snooze (re-show after cooldown)
 * - ✕ / repeated snoozes → dismiss (stop asking)
 */

const STORAGE_KEY = "multica.browserNotificationPrompt";

/** After this many "later" snoozes, treat as permanent dismiss (多次忽略). */
export const BROWSER_NOTIFICATION_PROMPT_SNOOZE_DISMISS_AFTER = 3;

export type BrowserNotificationPromptDecision =
  | { status: "undecided" }
  | { status: "dismissed"; at: number }
  | { status: "snoozed"; until: number; count: number };

type StorageLike = {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem?(key: string): void;
};

type StoredPrompt = {
  status?: string;
  at?: number;
  until?: number;
  count?: number;
};

function getStorage(): StorageLike | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function parseStored(storage: StorageLike): StoredPrompt | null {
  try {
    const raw = storage.getItem(STORAGE_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as StoredPrompt;
  } catch {
    return null;
  }
}

export function readBrowserNotificationPromptDecision(
  now = Date.now(),
  storage: StorageLike | null = getStorage(),
): BrowserNotificationPromptDecision {
  if (!storage) return { status: "undecided" };
  const parsed = parseStored(storage);
  if (!parsed) return { status: "undecided" };
  if (parsed.status === "dismissed" && typeof parsed.at === "number") {
    return { status: "dismissed", at: parsed.at };
  }
  if (parsed.status === "snoozed" && typeof parsed.until === "number") {
    const count = typeof parsed.count === "number" ? parsed.count : 1;
    if (parsed.until > now) return { status: "snoozed", until: parsed.until, count };
    // Expired: if already at dismiss threshold, keep quiet.
    if (count >= BROWSER_NOTIFICATION_PROMPT_SNOOZE_DISMISS_AFTER) {
      return { status: "dismissed", at: parsed.until };
    }
    return { status: "undecided" };
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

/**
 * Snooze for 3 days — "以后再说" / Later.
 * After {@link BROWSER_NOTIFICATION_PROMPT_SNOOZE_DISMISS_AFTER} snoozes,
 * escalate to permanent dismiss (多次忽略 → 不再提示).
 */
export function snoozeBrowserNotificationPrompt(
  storage: StorageLike | null = getStorage(),
  now = Date.now(),
  snoozeMs = 3 * 24 * 60 * 60 * 1000,
): void {
  if (!storage) return;
  const stored = parseStored(storage);
  let prevCount = 0;
  if (stored?.status === "snoozed" && typeof stored.count === "number") {
    prevCount = stored.count;
  } else if (stored?.status === "snoozed") {
    prevCount = 1;
  }
  const count = prevCount + 1;
  if (count >= BROWSER_NOTIFICATION_PROMPT_SNOOZE_DISMISS_AFTER) {
    dismissBrowserNotificationPrompt(storage, now);
    return;
  }
  storage.setItem(
    STORAGE_KEY,
    JSON.stringify({ status: "snoozed", until: now + snoozeMs, count }),
  );
}

export function clearBrowserNotificationPromptDecision(
  storage: StorageLike | null = getStorage(),
): void {
  storage?.removeItem?.(STORAGE_KEY);
}

export const BROWSER_NOTIFICATION_PROMPT_STORAGE_KEY = STORAGE_KEY;
