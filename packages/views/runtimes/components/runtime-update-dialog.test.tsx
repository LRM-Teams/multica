// @vitest-environment jsdom
import { render, screen, fireEvent, act, waitFor, within } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type {
  AgentRuntime,
  RuntimeHealthState,
  RuntimeUpdateState,
} from "@multica/core/types";
import enRuntimes from "../../locales/en/runtimes.json";

const initiateUpdate = vi.hoisted(() => vi.fn());
const getUpdateResult = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({
  api: { initiateUpdate, getUpdateResult },
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: unknown) => unknown) =>
    selector({ user: { id: "user-1" } }),
}));

const mockRuntimes = vi.hoisted(() => ({ current: [] as AgentRuntime[] }));
vi.mock("@multica/core/runtimes/queries", () => ({
  runtimeListOptions: (wsId: string) => ({
    queryKey: ["runtimes", wsId, "list"],
    queryFn: async () => mockRuntimes.current,
  }),
  runtimeKeys: { all: (wsId: string) => ["runtimes", wsId] },
}));

import { RuntimeUpdateDialog } from "./runtime-update-dialog";

function makeRuntime(
  overrides: {
    updateState?: RuntimeUpdateState;
    health?: RuntimeHealthState;
  } = {},
): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "My Mac",
    runtime_mode: "local",
    provider: "local",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: "0.3.64",
    target_version: "0.3.65",
    update_state: overrides.updateState ?? "idle",
    runtime_health: overrides.health ?? "update_available",
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
  };
}

function renderDialog(qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  render(
    <I18nProvider locale="en" resources={{ en: { runtimes: enRuntimes } }}>
      <QueryClientProvider client={qc}>
        <RuntimeUpdateDialog wsId="ws-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return qc;
}

// Flush the runtime-list query and pending microtasks so the prompt renders.
async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  mockRuntimes.current = [makeRuntime()];
});

afterEach(() => {
  vi.useRealTimers();
});

describe("RuntimeUpdateDialog (#687)", () => {
  it("prompts for an updatable runtime with an Update button", async () => {
    renderDialog();
    expect(await screen.findByText("Update Multica CLI")).toBeInTheDocument();
    expect(screen.getByText("Update now")).toBeInTheDocument();
    expect(screen.queryByText("Updating...")).toBeNull();
  });

  it("shows brief labeled progress in an aria-live region after clicking (no black window)", async () => {
    initiateUpdate.mockResolvedValue({ id: "upd-1", status: "running" });
    getUpdateResult.mockResolvedValue({ status: "running" });
    renderDialog();

    fireEvent.click(await screen.findByText("Update now"));
    await flush();

    const status = screen.getByRole("status");
    expect(within(status).getByText("Updating...")).toBeInTheDocument();
  });

  it("hands off naturally: refreshing flips eligibility so the prompt self-dismisses", async () => {
    // Once the update starts, the projection reports health "updating", which
    // drops the runtime from update eligibility.
    initiateUpdate.mockImplementation(async () => {
      mockRuntimes.current = [makeRuntime({ health: "updating" })];
      return { id: "upd-1", status: "running" };
    });
    getUpdateResult.mockResolvedValue({ status: "running" });
    renderDialog();

    fireEvent.click(await screen.findByText("Update now"));

    // The prompt disappears on its own — no pinned modal over the drain window.
    await waitFor(() =>
      expect(screen.queryByText("Update Multica CLI")).toBeNull(),
    );
  });

  it("treats ready_to_apply as terminal: stops the hidden poll and refreshes the projection", async () => {
    vi.useFakeTimers();
    initiateUpdate.mockResolvedValue({ id: "upd-1", status: "running" });
    getUpdateResult.mockResolvedValue({ status: "ready_to_apply" });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
    renderDialog(qc);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    fireEvent.click(screen.getByText("Update now"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0); // resolve initiate (+ handoff refresh)
      await vi.advanceTimersByTimeAsync(2000); // one poll tick -> ready_to_apply
    });

    // Refreshed on terminal so global surfaces reflect the staged state.
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["runtimes", "ws-1"] });

    const callsAfterTerminal = getUpdateResult.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    // Poll stopped once ready_to_apply hit — no forever-spin.
    expect(getUpdateResult.mock.calls.length).toBe(callsAfterTerminal);
  });

  it("shows an error and a Retry action when initiating the update fails", async () => {
    initiateUpdate.mockRejectedValue(new Error("disk full"));
    renderDialog();

    fireEvent.click(await screen.findByText("Update now"));
    await flush();

    expect(screen.getByText("disk full")).toBeInTheDocument();
    expect(screen.getByText("Retry")).toBeInTheDocument();
  });
});
