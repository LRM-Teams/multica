import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const { ApiError, getDevicePending, confirmDevice, logout } = vi.hoisted(() => {
  class ApiError extends Error {
    status: number;
    statusText: string;
    body: unknown;
    constructor(message: string, status: number, statusText: string, body?: unknown) {
      super(message);
      this.status = status;
      this.statusText = statusText;
      this.body = body;
    }
  }
  return {
    ApiError,
    getDevicePending: vi.fn(),
    confirmDevice: vi.fn(),
    logout: vi.fn(),
  };
});

vi.mock("@multica/core/api", () => ({
  api: { getDevicePending, confirmDevice },
  ApiError,
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (s: unknown) => unknown) => {
      const state = { user: { timezone: "UTC" } };
      return selector ? selector(state) : state;
    },
    { getState: () => ({ user: { timezone: "UTC" } }) },
  ),
}));

vi.mock("../auth", () => ({
  useLogout: () => logout,
}));

vi.mock("../platform", () => ({
  DragStrip: () => null,
}));

import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enDevice from "../locales/en/device.json";
import { DeviceConfirmPage } from "./device-confirm-page";

const TEST_RESOURCES = { en: { common: enCommon, device: enDevice } };

function renderPage(userCode: string | null) {
  const qc = new QueryClient();
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <DeviceConfirmPage userCode={userCode} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("DeviceConfirmPage (task #36)", () => {
  beforeEach(() => {
    getDevicePending.mockReset();
    confirmDevice.mockReset();
    logout.mockReset();
  });

  it("no user_code in the URL — shows the expired/invalid state, never a text input", async () => {
    renderPage(null);
    expect(await screen.findByText(/expired or invalid/i)).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(getDevicePending).not.toHaveBeenCalled();
  });

  it("valid code — fetches pending info and renders the client hint + approve/deny buttons", async () => {
    getDevicePending.mockResolvedValue({
      client_hint: "frank-laptop",
      created_at: new Date().toISOString(),
    });
    renderPage("WDJB-MJHT");
    expect(await screen.findByText(/frank-laptop/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Deny" })).toBeInTheDocument();
    expect(getDevicePending).toHaveBeenCalledWith("WDJB-MJHT");
  });

  it("empty client_hint — falls back to the unknown-device copy instead of rendering an empty sentence", async () => {
    getDevicePending.mockResolvedValue({
      client_hint: "",
      created_at: new Date().toISOString(),
    });
    renderPage("WDJB-MJHT");
    expect(await screen.findByText(/unknown device/i)).toBeInTheDocument();
  });

  it("404 from the pending fetch — shows the expired/invalid state (never a raw error or an input)", async () => {
    getDevicePending.mockRejectedValue(new ApiError("not found", 404, "Not Found"));
    renderPage("STALE-CODE");
    expect(await screen.findByText(/expired or invalid/i)).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });



});
