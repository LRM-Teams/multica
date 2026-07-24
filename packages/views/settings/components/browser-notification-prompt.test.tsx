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
    expect(platform.request).toHaveBeenCalled();
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
});
