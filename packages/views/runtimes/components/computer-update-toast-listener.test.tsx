import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ComputerUpdateToastListener } from "./computer-update-toast-listener";

const mocks = vi.hoisted(() => ({
  runtimes: [] as AgentRuntime[],
  toastCustom: vi.fn(),
  toastDismiss: vi.fn(),
  initiateMachineUpgrade: vi.fn(),
  invalidateQueries: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  queryOptions: <T,>(options: T) => options,
  useQuery: () => ({ data: mocks.runtimes }),
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
}));

vi.mock("sonner", () => ({
  toast: {
    custom: mocks.toastCustom,
    dismiss: mocks.toastDismiss,
  },
}));

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    initiateMachineUpgrade: mocks.initiateMachineUpgrade,
    getMachineUpgrade: vi.fn(),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (
    selector: (state: { user: { id: string } }) => unknown,
  ) => selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

function s143Runtime(): AgentRuntime {
  return {
    id: "0ad7ac57-7c84-45b4-a69c-d6591116230b",
    workspace_id: "workspace-1",
    daemon_id: "1298b34b-b7de-4309-bdfb-71043265052d",
    name: "s143",
    display_name: "s143",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: "0.4.24-alpha.11",
    target_version: "v0.4.24-alpha.12",
    daemon_target_version: "v0.4.24-alpha.12",
    update_state: "idle",
    runtime_health: "update_available",
    machine_upgrade: {
      id: "machine-upgrade-alpha-11",
      daemon_id: "1298b34b-b7de-4309-bdfb-71043265052d",
      request_id: "request-alpha-11",
      requested_target: "v0.4.24-alpha.11",
      resolved_target: "v0.4.24-alpha.11",
      phase: "completed",
      result: "completed",
      created_at: "2026-08-11T13:00:00Z",
      updated_at: "2026-08-11T13:05:00Z",
    },
    owner_id: "user-1",
    last_seen_at: new Date().toISOString(),
    created_at: "2026-08-11T12:00:00Z",
    updated_at: "2026-08-11T13:05:00Z",
  };
}

describe("ComputerUpdateToastListener", () => {
  beforeEach(() => {
    mocks.runtimes = [s143Runtime()];
    mocks.toastCustom.mockReset();
    mocks.toastDismiss.mockReset();
    mocks.initiateMachineUpgrade.mockReset();
    mocks.initiateMachineUpgrade.mockImplementation(
      () => new Promise(() => {}),
    );
    mocks.invalidateQueries.mockReset();
    window.localStorage.clear();
  });

  it("shows and submits the newer daemon target after a completed upgrade", async () => {
    const user = userEvent.setup();
    renderWithI18n(<ComputerUpdateToastListener />);

    await waitFor(() => expect(mocks.toastCustom).toHaveBeenCalled());
    const renderToast = mocks.toastCustom.mock.calls[0]?.[0] as (
      id: string | number,
    ) => ReactNode;
    render(renderToast("computer-update:s143"));

    expect(
      screen.getByText("0.4.24-alpha.11 → v0.4.24-alpha.12"),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Update now" }));

    expect(mocks.initiateMachineUpgrade).toHaveBeenCalledWith(
      "1298b34b-b7de-4309-bdfb-71043265052d",
      "v0.4.24-alpha.12",
      expect.any(String),
    );
  });
});
