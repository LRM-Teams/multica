import { describe, expect, it } from "vitest";
import {
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
});
