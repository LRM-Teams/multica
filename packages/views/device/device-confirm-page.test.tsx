import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

describe("DeviceConfirmPage (RFC 8628 verification)", () => {
  beforeEach(() => {
    getDevicePending.mockReset();
    confirmDevice.mockReset();
    logout.mockReset();
  });

  it("no user_code in the URL — shows a typable submit, does not fetch yet", async () => {
    renderPage(null);
    expect(await screen.findByLabelText(/device code/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /continue/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
    expect(getDevicePending).not.toHaveBeenCalled();
  });

  it("typed submit — looks up the code then shows approve/deny", async () => {
    const user = userEvent.setup();
    getDevicePending.mockResolvedValue({
      client_hint: "Multica CLI",
      created_at: new Date().toISOString(),
    });
    renderPage(null);
    await user.type(screen.getByLabelText(/device code/i), "WDJB-MJHT");
    await user.click(screen.getByRole("button", { name: /continue/i }));
    expect(await screen.findByRole("button", { name: "Approve" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
    expect(getDevicePending).toHaveBeenCalledWith("WDJB-MJHT");
  });

  it("complete URI — shows the code and requires a match confirm before approve/deny", async () => {
    const user = userEvent.setup();
    getDevicePending.mockResolvedValue({
      client_hint: "Multica CLI",
      created_at: new Date().toISOString(),
    });
    renderPage("WDJB-MJHT");
    expect(await screen.findByText("WDJB-MJHT")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: /matches the code on my device/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Deny" })).toBeDisabled();

    await user.click(screen.getByRole("checkbox", { name: /matches the code on my device/i }));
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
  });

  it("empty client_hint — falls back to the unknown-device copy instead of rendering an empty sentence", async () => {
    getDevicePending.mockResolvedValue({
      client_hint: "",
      created_at: new Date().toISOString(),
    });
    renderPage("WDJB-MJHT");
    expect(await screen.findByText(/unknown device/i)).toBeInTheDocument();
  });

  it("404 from the pending fetch on a complete URI — shows the expired/invalid state", async () => {
    getDevicePending.mockRejectedValue(new ApiError("not found", 404, "Not Found"));
    renderPage("STALE-CODE");
    expect(await screen.findByText(/expired or invalid/i)).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("typed submit 404 — stays on the type-in form with an invalid-code error", async () => {
    const user = userEvent.setup();
    getDevicePending.mockRejectedValue(new ApiError("not found", 404, "Not Found"));
    renderPage(null);
    await user.type(screen.getByLabelText(/device code/i), "ZZZZ-ZZZZ");
    await user.click(screen.getByRole("button", { name: /continue/i }));
    expect(await screen.findByText(/invalid or expired/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/device code/i)).toBeInTheDocument();
  });

  it("approve after match confirm — calls confirmDevice(userCode, true)", async () => {
    const user = userEvent.setup();
    getDevicePending.mockResolvedValue({
      client_hint: "frank-laptop",
      created_at: new Date().toISOString(),
    });
    confirmDevice.mockResolvedValue({ status: "approved" });
    renderPage("WDJB-MJHT");
    await user.click(await screen.findByRole("checkbox", { name: /matches the code on my device/i }));
    await user.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => {
      expect(screen.getByText(/sign-in approved/i)).toBeInTheDocument();
    });
    expect(confirmDevice).toHaveBeenCalledWith("WDJB-MJHT", true);
  });

  it("deny after match confirm — calls confirmDevice(userCode, false)", async () => {
    const user = userEvent.setup();
    getDevicePending.mockResolvedValue({
      client_hint: "frank-laptop",
      created_at: new Date().toISOString(),
    });
    confirmDevice.mockResolvedValue({ status: "denied" });
    renderPage("WDJB-MJHT");
    await user.click(await screen.findByRole("checkbox", { name: /matches the code on my device/i }));
    await user.click(screen.getByRole("button", { name: "Deny" }));
    await waitFor(() => {
      expect(screen.getByText(/sign-in denied/i)).toBeInTheDocument();
    });
    expect(confirmDevice).toHaveBeenCalledWith("WDJB-MJHT", false);
  });

  it("confirm races an expiry (404) — falls back to the expired/invalid state, not a raw error", async () => {
    const user = userEvent.setup();
    getDevicePending.mockResolvedValue({
      client_hint: "frank-laptop",
      created_at: new Date().toISOString(),
    });
    confirmDevice.mockRejectedValue(new ApiError("not found", 404, "Not Found"));
    renderPage("WDJB-MJHT");
    await user.click(await screen.findByRole("checkbox", { name: /matches the code on my device/i }));
    await user.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => {
      expect(screen.getByText(/expired or invalid/i)).toBeInTheDocument();
    });
  });
});
