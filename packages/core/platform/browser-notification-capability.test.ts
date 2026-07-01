import { afterEach, describe, expect, it } from "vitest";
import {
  getBrowserNotificationCapability,
  isIOSBrowser,
  isStandaloneDisplayMode,
} from "./browser-notification-capability";

type TestNavigator = Partial<Navigator> & { standalone?: boolean };

function installBrowser({
  userAgent = "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/125 Mobile Safari/537.36 EdgA/125",
  platform = "Linux armv8l",
  maxTouchPoints = 5,
  standalone = false,
  displayStandalone = false,
  push = true,
}: {
  userAgent?: string;
  platform?: string;
  maxTouchPoints?: number;
  standalone?: boolean;
  displayStandalone?: boolean;
  push?: boolean;
} = {}) {
  const navigatorStub: TestNavigator = {
    userAgent,
    platform,
    maxTouchPoints,
    standalone,
  };
  if (push) {
    (navigatorStub as unknown as Record<string, unknown>).serviceWorker = {};
  }
  const windowStub: Partial<Window> & { PushManager?: unknown; Notification?: unknown } = {
    Notification: function Notification() {},
    matchMedia: () => ({ matches: displayStandalone }) as MediaQueryList,
  };
  if (push) {
    windowStub.PushManager = function PushManager() {};
  }
  Object.defineProperty(globalThis, "navigator", {
    value: navigatorStub,
    configurable: true,
  });
  Object.defineProperty(globalThis, "window", {
    value: windowStub,
    configurable: true,
  });
}

afterEach(() => {
  delete (globalThis as Record<string, unknown>).navigator;
  delete (globalThis as Record<string, unknown>).window;
});

describe("browser notification capability", () => {
  it("classifies Android Chrome/Edge as standard web notification support", () => {
    installBrowser();
    expect(isIOSBrowser()).toBe(false);
    expect(getBrowserNotificationCapability()).toBe("standard");
  });

  it("requires Home Screen mode for iOS browsers", () => {
    installBrowser({
      userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1",
      platform: "iPhone",
    });
    expect(isIOSBrowser()).toBe(true);
    expect(isStandaloneDisplayMode()).toBe(false);
    expect(getBrowserNotificationCapability()).toBe("ios-needs-pwa");
  });

  it("allows iOS once running as an installed PWA", () => {
    installBrowser({
      userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1",
      platform: "iPhone",
      displayStandalone: true,
    });
    expect(isStandaloneDisplayMode()).toBe(true);
    expect(getBrowserNotificationCapability()).toBe("ios-pwa");
  });

  it("reports unsupported when push primitives are missing", () => {
    installBrowser({ push: false });
    expect(getBrowserNotificationCapability()).toBe("unsupported");
  });
});
