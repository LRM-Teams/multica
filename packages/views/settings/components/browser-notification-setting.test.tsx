import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { BrowserNotificationSetting } from "./browser-notification-setting";

const platform = vi.hoisted(() => ({
  isDesktopShell: false,
  permission: "granted" as string,
  supported: true,
}));

const webpush = vi.hoisted(() => ({
  supportState: "supported" as string,
  requestAndBind: vi.fn(async () => ({ endpoint: "https://push/ep" })),
  bindCurrent: vi.fn(async () => ({ endpoint: "https://push/ep" })),
}));

const api = vi.hoisted(() => ({
  sendTestWebPush: vi.fn(async () => ({
    ok: true,
    delivered: 1,
    failed: 0,
    gone: 0,
    attempted: 1,
  })),
}));

const toast = vi.hoisted(() => ({
  success: vi.fn(),
}));

const showErrorToast = vi.hoisted(() => vi.fn());

vi.mock("../../platform", () => ({
  isDesktopShell: () => platform.isDesktopShell,
}));

vi.mock("@multica/core/platform", () => ({
  getWebNotificationPermission: () => platform.permission,
  isWebNotificationSupported: () => platform.supported,
}));

vi.mock("@multica/core/web-push", () => ({
  getWebPushSupportState: () => webpush.supportState,
  requestAndBindWebPushSubscription: () => webpush.requestAndBind(),
  bindCurrentWebPushSubscription: () => webpush.bindCurrent(),
}));

vi.mock("@multica/core/api", () => {
  class ApiError extends Error {
    status: number;
    statusText: string;
    constructor(message: string, status = 500, statusText = "Error") {
      super(message);
      this.name = "ApiError";
      this.status = status;
      this.statusText = statusText;
    }
  }
  return { api, ApiError };
});

vi.mock("sonner", () => ({ toast }));

vi.mock("@multica/ui/lib/error-toast", () => ({ showErrorToast }));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (bundle: Record<string, unknown>) => unknown) => {
      const bundle = {
        notifications: {
          browser: {
            label: "Browser notifications",
            hint: "hint",
            enable: "Enable",
            granted: "granted",
            denied: "denied",
            enabled_badge: "Enabled",
            unsupported: "unsupported",
            ios_requires_pwa: "ios",
            test: {
              send: "Send test notification",
              sending: "Sending…",
              success: "Sent — check your system notification banner.",
              failed_generic: "Could not send the test notification.",
              need_permission: "Enable browser notifications first to send a test.",
            },
          },
        },
      };
      return selector(bundle);
    },
  }),
}));

describe("BrowserNotificationSetting test push (LRM-755)", () => {
  beforeEach(() => {
    platform.isDesktopShell = false;
    platform.permission = "granted";
    platform.supported = true;
    webpush.supportState = "supported";
    webpush.bindCurrent.mockClear();
    webpush.requestAndBind.mockClear();
    api.sendTestWebPush.mockClear();
    api.sendTestWebPush.mockResolvedValue({
      ok: true,
      delivered: 1,
      failed: 0,
      gone: 0,
      attempted: 1,
    });
    toast.success.mockClear();
    showErrorToast.mockClear();
  });


  it("disables the test button when permission is not granted and shows guide", () => {
    platform.permission = "default";
    render(<BrowserNotificationSetting />);
    expect(screen.getByTestId("browser-notification-send-test")).toBeDisabled();
    expect(
      screen.getByText("Enable browser notifications first to send a test."),
    ).toBeInTheDocument();
  });

});
