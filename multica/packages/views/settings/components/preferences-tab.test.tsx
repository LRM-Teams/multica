import type { ReactNode } from "react";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, act, cleanup, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAuth from "../../locales/en/auth.json";
import enSettings from "../../locales/en/settings.json";

const mockPersist = vi.hoisted(() => vi.fn());
const mockUpdateMe = vi.hoisted(() => vi.fn());
const mockReload = vi.hoisted(() => vi.fn());
const mockToastWarning = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());
const mockSetUser = vi.hoisted(() => vi.fn());
const userRef = vi.hoisted(() => ({
  current: null as { id: string; timezone?: string | null } | null,
}));

vi.mock("@multica/ui/components/common/theme-provider", () => ({
  useTheme: () => ({ theme: "light", setTheme: vi.fn() }),
}));

vi.mock("@multica/core/i18n/react", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/i18n/react")>(
      "@multica/core/i18n/react",
    );
  return {
    ...actual,
    useLocaleAdapter: () => ({
      persist: mockPersist,
      getUserChoice: () => null,
      getSystemPreferences: () => [],
    }),
  };
});

vi.mock("@multica/core/api", () => ({
  api: { updateMe: mockUpdateMe },
}));

vi.mock("sonner", () => ({
  toast: { warning: mockToastWarning, error: mockToastError },
}));

vi.mock("@multica/core/auth", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/auth")>(
      "@multica/core/auth",
    );
  type AuthState = {
    user: typeof userRef.current;
    setUser: typeof mockSetUser;
  };
  const state = (): AuthState => ({
    user: userRef.current,
    setUser: mockSetUser,
  });
  const useAuthStore = Object.assign(
    (sel?: (s: AuthState) => unknown) =>
      sel ? sel(state()) : state(),
    { getState: state },
  );
  return { ...actual, useAuthStore };
});

// The timezone <Select> renders every option from `timezoneOptions`, which
// enumerates ~600 IANA zones. Querying that list through the Base UI popup is
// what made these tests time out on slow CI (task #298). Stub the source to a
// handful of zones — these tests exercise pick→PATCH→store, not the IANA
// catalogue — so they run fast and deterministically.
vi.mock("../../common/timezone-select", async () => {
  const actual =
    await vi.importActual<typeof import("../../common/timezone-select")>(
      "../../common/timezone-select",
    );
  const ZONES = ["Asia/Shanghai", "Asia/Tokyo", "America/New_York", "UTC"];
  return {
    ...actual,
    browserTimezone: () => "Asia/Shanghai",
    timezoneOptions: (current: string) =>
      ZONES.includes(current) ? ZONES : [current, ...ZONES],
  };
});

import { PreferencesTab } from "./preferences-tab";

const TEST_RESOURCES = {
  en: { common: enCommon, auth: enAuth, settings: enSettings },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

describe("PreferencesTab — Theme preview tokens (LRM-355)", () => {
  it("does not ship a private LIGHT_COLORS / DARK_COLORS hex table", () => {
    const src = readFileSync(
      resolve(dirname(fileURLToPath(import.meta.url)), "./preferences-tab.tsx"),
      "utf8",
    );
    expect(src).not.toMatch(/\bLIGHT_COLORS\b/);
    expect(src).not.toMatch(/\bDARK_COLORS\b/);
    // Product surfaces must stay on semantic utilities, not inline hex fills
    // (OS traffic-light dots are the only intentional hex in this file).
    expect(src).not.toMatch(/backgroundColor:\s*colors\./);
    expect(src).toMatch(/bg-background/);
    expect(src).toMatch(/bg-sidebar/);
    expect(src).toMatch(/bg-muted/);
  });

  it("scopes Light / Dark / System mockups with light|dark token roots", () => {
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    const lightPreviews = document.querySelectorAll(
      '[data-theme-preview="light"]',
    );
    const darkPreviews = document.querySelectorAll(
      '[data-theme-preview="dark"]',
    );
    // Light card + System left half; Dark card + System right half
    expect(lightPreviews.length).toBe(2);
    expect(darkPreviews.length).toBe(2);

    for (const el of lightPreviews) {
      expect(el.classList.contains("light")).toBe(true);
      expect(el.classList.contains("dark")).toBe(false);
    }
    for (const el of darkPreviews) {
      expect(el.classList.contains("dark")).toBe(true);
      expect(el.classList.contains("light")).toBe(false);
    }

    expect(
      screen.getByRole("radio", { name: "Light" }),
    ).toBeTruthy();
    expect(screen.getByRole("radio", { name: "Dark" })).toBeTruthy();
    expect(screen.getByRole("radio", { name: "System" })).toBeTruthy();
  });
});

describe("PreferencesTab — Language switcher", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    userRef.current = null;
    vi.useFakeTimers({ shouldAdvanceTime: true });
    Object.defineProperty(window, "location", {
      writable: true,
      configurable: true,
      value: { reload: mockReload },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("does nothing when clicking the current locale", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("radio", { name: "English" }));

    expect(mockPersist).not.toHaveBeenCalled();
    expect(mockUpdateMe).not.toHaveBeenCalled();
    expect(mockReload).not.toHaveBeenCalled();
  });

  it("when not logged in: persists + reloads, no PATCH", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("radio", { name: "한국어" }));

    expect(mockPersist).toHaveBeenCalledWith("ko");
    expect(mockUpdateMe).not.toHaveBeenCalled();
    expect(mockReload).toHaveBeenCalledTimes(1);
    expect(mockToastWarning).not.toHaveBeenCalled();
  });

  it("when not logged in: selecting Japanese persists ja + reloads, no PATCH", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("radio", { name: "日本語" }));

    expect(mockPersist).toHaveBeenCalledWith("ja");
    expect(mockUpdateMe).not.toHaveBeenCalled();
    expect(mockReload).toHaveBeenCalledTimes(1);
    expect(mockToastWarning).not.toHaveBeenCalled();
  });

  it("when logged in + PATCH success: persists + PATCH + reload immediately", async () => {
    userRef.current = { id: "user-1" };
    mockUpdateMe.mockResolvedValueOnce({});
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("radio", { name: "中文" }));

    expect(mockPersist).toHaveBeenCalledWith("zh-Hans");
    expect(mockUpdateMe).toHaveBeenCalledWith({ language: "zh-Hans" });
    expect(mockToastWarning).not.toHaveBeenCalled();
    expect(mockReload).toHaveBeenCalledTimes(1);
  });

  it("when logged in + PATCH fails: shows toast and delays reload by 2.5s", async () => {
    userRef.current = { id: "user-1" };
    mockUpdateMe.mockRejectedValueOnce(new Error("network"));
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("radio", { name: "中文" }));

    // Local persist still happened so the reload below sees the new locale.
    expect(mockPersist).toHaveBeenCalledWith("zh-Hans");
    expect(mockUpdateMe).toHaveBeenCalledWith({ language: "zh-Hans" });
    // Toast surfaced the sync failure.
    expect(mockToastWarning).toHaveBeenCalledTimes(1);
    // Reload deferred so the toast is visible.
    expect(mockReload).not.toHaveBeenCalled();

    act(() => {
      vi.advanceTimersByTime(2500);
    });
    expect(mockReload).toHaveBeenCalledTimes(1);
  });
});

describe("PreferencesTab — Timezone section", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    userRef.current = null;
  });

  // Base UI Select portals its popup onto document.body; unmount each
  // render fully between tests so a prior test's trigger/popup can't
  // shadow the next one's.
  afterEach(() => {
    cleanup();
  });

  // Opens the Select popup and clicks the option whose accessible name
  // matches. Re-queries the trigger each call so it operates on the
  // current render, never a stale node.
  async function pickTimezone(
    user: ReturnType<typeof userEvent.setup>,
    name: RegExp | string,
  ) {
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name }));
  }

  it("renders the stored timezone in the trigger", () => {
    userRef.current = { id: "user-1", timezone: "Asia/Shanghai" };
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    expect(screen.getByRole("combobox").textContent).toContain("Asia/Shanghai");
  });

  // handleChange PATCHes then updates the store asynchronously, so the
  // post-pick assertions must waitFor it to settle. The extended timeout
  // covers querying the Select's full ~600-option IANA list on slow CI.
  it("saving a new timezone PATCHes /api/me and updates the auth store", async () => {
    userRef.current = { id: "user-1", timezone: "Asia/Shanghai" };
    const updatedUser = { id: "user-1", timezone: "Asia/Tokyo" };
    mockUpdateMe.mockResolvedValueOnce(updatedUser);
    const user = userEvent.setup();
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    await pickTimezone(user, "Asia/Tokyo");

    await waitFor(() => {
      expect(mockUpdateMe).toHaveBeenCalledWith({ timezone: "Asia/Tokyo" });
      expect(mockSetUser).toHaveBeenCalledWith(updatedUser);
    });
  });

  it("surfaces a toast when the PATCH fails", async () => {
    userRef.current = { id: "user-1", timezone: "Asia/Shanghai" };
    mockUpdateMe.mockRejectedValueOnce(new Error("network down"));
    const user = userEvent.setup();
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    await pickTimezone(user, "Asia/Tokyo");

    await waitFor(() => {
      expect(mockUpdateMe).toHaveBeenCalledWith({ timezone: "Asia/Tokyo" });
      expect(mockToastError).toHaveBeenCalledTimes(1);
    });
    expect(mockSetUser).not.toHaveBeenCalled();
  });

  it("clearing the preference sends an empty-string timezone", async () => {
    userRef.current = { id: "user-1", timezone: "Asia/Shanghai" };
    const clearedUser = { id: "user-1", timezone: null };
    mockUpdateMe.mockResolvedValueOnce(clearedUser);
    const user = userEvent.setup();
    render(<PreferencesTab />, { wrapper: I18nWrapper });

    // The "(browser)" sentinel option resets the preference to NULL; the
    // wire payload is an empty string the backend translates to NULL.
    await pickTimezone(user, /browser/i);

    await waitFor(() => {
      expect(mockUpdateMe).toHaveBeenCalledWith({ timezone: "" });
      // The PATCH response (timezone: null) is pushed into the auth store
      // so the picker switches back to "(browser)" without a refetch.
      expect(mockSetUser).toHaveBeenCalledWith(clearedUser);
    });
  });
});
