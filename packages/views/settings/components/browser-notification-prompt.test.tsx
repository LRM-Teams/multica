import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { BrowserNotificationPrompt } from "./browser-notification-prompt";

const platform = vi.hoisted(() => ({
  isDesktopShell: false,
  permission: "default" as string,
  capability: "standard" as string,
  shouldShow: true,
  request: vi.fn(async () => "granted"),
  dismiss: vi.fn(),
  snooze: vi.fn(),
}));

vi.mock("../../platform", () => ({
  isDesktopShell: () => platform.isDesktopShell,
}));

vi.mock("@multica/core/platform", () => ({
  getWebNotificationPermission: () => platform.permission,
  getBrowserNotificationCapability: () => platform.capability,
  shouldShowBrowserNotificationPrompt: () => platform.shouldShow,
  requestWebNotificationPermission: () => platform.request(),
  dismissBrowserNotificationPrompt: () => platform.dismiss(),
  snoozeBrowserNotificationPrompt: () => platform.snooze(),
}));

const webpush = vi.hoisted(() => ({
  supportState: "supported" as string,
  requestAndBind: vi.fn(async () => ({
    endpoint: "https://push/ep",
    keys: { p256dh: "p", auth: "a" },
    expiration_time: null,
    device_id: "https://push/ep",
    user_agent: "",
  })),
  bindCurrent: vi.fn(async () => ({
    endpoint: "https://push/ep",
    keys: { p256dh: "p", auth: "a" },
    expiration_time: null,
    device_id: "https://push/ep",
    user_agent: "",
  })),
}));

vi.mock("@multica/core/web-push", () => ({
  getWebPushSupportState: () => webpush.supportState,
  requestAndBindWebPushSubscription: () => webpush.requestAndBind(),
  bindCurrentWebPushSubscription: () => webpush.bindCurrent(),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (bundle: Record<string, unknown>) => unknown) => {
      const bundle = {
        notifications: {
          browser: {
            prompt: {
              title: "Turn on notifications",
              description:
                "Get notified about DMs and @mentions even when Multica is in the background.",
              enable: "Enable notifications",
              later: "Maybe later",
              dismiss: "Don't ask again",
            },
          },
        },
      };
      return selector(bundle);
    },
  }),
}));

describe("BrowserNotificationPrompt (LRM-525)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    platform.isDesktopShell = false;
    platform.permission = "default";
    platform.capability = "standard";
    platform.shouldShow = true;
    platform.request.mockClear();
    platform.dismiss.mockClear();
    platform.snooze.mockClear();
    webpush.supportState = "supported";
    webpush.requestAndBind.mockClear();
    webpush.bindCurrent.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders Slack-style top banner (not a dialog) with two actions + ✕", async () => {
    render(<BrowserNotificationPrompt />);
    await act(async () => {
      vi.advanceTimersByTime(800);
    });

    const banner = screen.getByTestId("browser-notification-prompt");
    expect(banner).toBeInTheDocument();
    expect(banner.tagName.toLowerCase()).toBe("section");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    const enable = screen.getByTestId("browser-notification-prompt-enable");
    expect(enable).toHaveClass("bg-brand");
    expect(enable).toHaveClass("h-11");
    expect(screen.getByTestId("browser-notification-prompt-later")).toBeInTheDocument();
    expect(
      screen.getByTestId("browser-notification-prompt-dismiss"),
    ).toBeInTheDocument();
    // No third heavy "don't ask again" text row — only ✕ aria.
    expect(screen.queryByText("Don't ask again")).not.toBeInTheDocument();
  });

  it("enable dismisses permanently; later snoozes; ✕ dismisses", async () => {
    const { unmount } = render(<BrowserNotificationPrompt />);
    await act(async () => {
      vi.advanceTimersByTime(800);
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId("browser-notification-prompt-enable"));
    });
    // Enable must subscribe + bind (not just request permission), or
    // background push never delivers. See LRM-679.
    expect(webpush.requestAndBind).toHaveBeenCalled();
    expect(platform.dismiss).toHaveBeenCalled();
    unmount();

    platform.dismiss.mockClear();
    platform.snooze.mockClear();
    platform.shouldShow = true;
    const later = render(<BrowserNotificationPrompt />);
    await act(async () => {
      vi.advanceTimersByTime(800);
    });
    fireEvent.click(screen.getByTestId("browser-notification-prompt-later"));
    expect(platform.snooze).toHaveBeenCalled();
    later.unmount();

    platform.shouldShow = true;
    render(<BrowserNotificationPrompt />);
    await act(async () => {
      vi.advanceTimersByTime(800);
    });
    fireEvent.click(screen.getByTestId("browser-notification-prompt-dismiss"));
    expect(platform.dismiss).toHaveBeenCalled();
  });

  it("stays hidden on desktop shell", async () => {
    platform.isDesktopShell = true;
    render(<BrowserNotificationPrompt />);
    await act(async () => {
      vi.advanceTimersByTime(800);
    });
    expect(
      screen.queryByTestId("browser-notification-prompt"),
    ).not.toBeInTheDocument();
  });

  it("ensures a push subscription is bound on mount when permission already granted (LRM-679 recovery)", async () => {
    // Frank already granted permission (e.g. via the old prompt that only
    // requested permission) but has no server-side subscription → background
    // push dark. On dashboard mount we must best-effort (re)bind.
    platform.permission = "granted";
    render(<BrowserNotificationPrompt />);
    await act(async () => {
      await Promise.resolve();
    });
    expect(webpush.bindCurrent).toHaveBeenCalled();
  });

  it("does not ensure-bind on mount when permission is not yet granted", async () => {
    platform.permission = "default";
    render(<BrowserNotificationPrompt />);
    await act(async () => {
      vi.advanceTimersByTime(800);
      await Promise.resolve();
    });
    expect(webpush.bindCurrent).not.toHaveBeenCalled();
  });

  it("does not ensure-bind on desktop shell", async () => {
    platform.isDesktopShell = true;
    platform.permission = "granted";
    render(<BrowserNotificationPrompt />);
    await act(async () => {
      await Promise.resolve();
    });
    expect(webpush.bindCurrent).not.toHaveBeenCalled();
  });
});
