import { describe, expect, it } from "vitest";
import {
  BROWSER_NOTIFICATION_PROMPT_SNOOZE_DISMISS_AFTER,
  dismissBrowserNotificationPrompt,
  readBrowserNotificationPromptDecision,
  shouldShowBrowserNotificationPrompt,
  snoozeBrowserNotificationPrompt,
} from "./browser-notification-prompt";

function memoryStorage(initial: Record<string, string> = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => {
      map.set(k, v);
    },
    removeItem: (k: string) => {
      map.delete(k);
    },
  };
}

describe("browser notification prompt decision", () => {
  it("shows when permission is default and undecided", () => {
    const storage = memoryStorage();
    expect(
      shouldShowBrowserNotificationPrompt("default", "standard", Date.now(), storage),
    ).toBe(true);
  });

  it("hides when already granted/denied/unsupported", () => {
    const storage = memoryStorage();
    expect(
      shouldShowBrowserNotificationPrompt("granted", "standard", Date.now(), storage),
    ).toBe(false);
    expect(
      shouldShowBrowserNotificationPrompt("denied", "standard", Date.now(), storage),
    ).toBe(false);
    expect(
      shouldShowBrowserNotificationPrompt("default", "unsupported", Date.now(), storage),
    ).toBe(false);
    expect(
      shouldShowBrowserNotificationPrompt(
        "default",
        "ios-needs-pwa",
        Date.now(),
        storage,
      ),
    ).toBe(false);
  });

  it("hides after dismiss and after active snooze", () => {
    const storage = memoryStorage();
    const now = 1_000_000;
    dismissBrowserNotificationPrompt(storage, now);
    expect(readBrowserNotificationPromptDecision(now, storage)).toEqual({
      status: "dismissed",
      at: now,
    });
    expect(
      shouldShowBrowserNotificationPrompt("default", "standard", now, storage),
    ).toBe(false);

    const storage2 = memoryStorage();
    snoozeBrowserNotificationPrompt(storage2, now, 60_000);
    expect(readBrowserNotificationPromptDecision(now + 1, storage2)).toEqual({
      status: "snoozed",
      until: now + 60_000,
      count: 1,
    });
    expect(
      shouldShowBrowserNotificationPrompt("default", "standard", now + 1, storage2),
    ).toBe(false);
    expect(
      shouldShowBrowserNotificationPrompt(
        "default",
        "standard",
        now + 60_001,
        storage2,
      ),
    ).toBe(true);
  });

  it("escalates the Nth later click to dismiss", () => {
    const storage = memoryStorage();
    let t = 1_000_000;
    for (let i = 1; i < BROWSER_NOTIFICATION_PROMPT_SNOOZE_DISMISS_AFTER; i++) {
      snoozeBrowserNotificationPrompt(storage, t, 1_000);
      const decision = readBrowserNotificationPromptDecision(t, storage);
      expect(decision).toMatchObject({ status: "snoozed", count: i });
      t += 2_000; // past snooze window
      expect(
        shouldShowBrowserNotificationPrompt("default", "standard", t, storage),
      ).toBe(true);
    }
    snoozeBrowserNotificationPrompt(storage, t, 1_000);
    expect(readBrowserNotificationPromptDecision(t, storage)).toEqual({
      status: "dismissed",
      at: t,
    });
    expect(
      shouldShowBrowserNotificationPrompt("default", "standard", t + 10_000, storage),
    ).toBe(false);
  });
});
