import { describe, it, expect, beforeEach, vi } from "vitest";
import { api, ApiError } from "../api";
import {
  useComputerUpgradeStore,
} from "./computer-upgrade-store";

vi.mock("../api", () => ({
  api: {
    initiateMachineUpgrade: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    body: unknown;
    constructor(message: string, status: number, _statusText: string, body: unknown) {
      super(message);
      this.status = status;
      this.body = body;
    }
  },
}));

describe("computer-upgrade-store", () => {
  beforeEach(() => {
    useComputerUpgradeStore.getState().reset();
    vi.clearAllMocks();
  });

  it("handles startUpgrade lifecycle successfully", async () => {
    vi.mocked(api.initiateMachineUpgrade).mockResolvedValueOnce({
      request_id: "req-123",
    });

    const promise = useComputerUpgradeStore.getState().startUpgrade({
      daemonId: "daemon-1",
      targetVersion: "v1.2.3",
      machineTitle: "Frank's Mac",
      requestId: "req-123",
    });

    // Check pending state before resolving
    const pending = useComputerUpgradeStore.getState().getUpgrade("daemon-1");
    expect(pending).toMatchObject({
      daemonId: "daemon-1",
      targetVersion: "v1.2.3",
      machineTitle: "Frank's Mac",
      requestId: "req-123",
      phase: "pending",
    });

    const reqId = await promise;
    expect(reqId).toBe("req-123");
    expect(api.initiateMachineUpgrade).toHaveBeenCalledWith(
      "daemon-1",
      "v1.2.3",
      "req-123",
    );

    const running = useComputerUpgradeStore.getState().getUpgrade("daemon-1");
    expect(running?.phase).toBe("running");
  });

  it("handles startUpgrade failure", async () => {
    vi.mocked(api.initiateMachineUpgrade).mockRejectedValueOnce(
      new Error("Network error"),
    );

    await expect(
      useComputerUpgradeStore.getState().startUpgrade({
        daemonId: "daemon-1",
        targetVersion: "v1.2.3",
      }),
    ).rejects.toThrow("Network error");

    const failed = useComputerUpgradeStore.getState().getUpgrade("daemon-1");
    expect(failed?.phase).toBe("failed");
    expect(failed?.error).toBe("Network error");
  });

  it("handles pinned runtime conflict in startUpgrade", async () => {
    vi.mocked(api.initiateMachineUpgrade).mockRejectedValueOnce(
      new ApiError("Conflict", 409, "Conflict", { code: "runtime_pinned" }),
    );

    await expect(
      useComputerUpgradeStore.getState().startUpgrade({
        daemonId: "daemon-1",
        targetVersion: "v1.2.3",
      }),
    ).rejects.toThrow();

    const upgrade = useComputerUpgradeStore.getState().getUpgrade("daemon-1");
    // When runtime is pinned, the update never started and is cleaned up from the store
    expect(upgrade).toBeUndefined();
  });

  it("records progress from WS event", () => {
    useComputerUpgradeStore.getState().recordProgress({
      computer_id: "daemon-1",
      requestId: "req-1",
      message: "Downloading binary...",
      percent: 45,
    });

    const upgrade = useComputerUpgradeStore.getState().getUpgrade("daemon-1");
    expect(upgrade?.phase).toBe("running");
    expect(upgrade?.progress).toBe("Downloading binary...");
    expect(upgrade?.percent).toBe(45);
  });

  it("records successful done from WS event", () => {
    useComputerUpgradeStore.getState().recordDone({
      computer_id: "daemon-1",
      requestId: "req-1",
      ok: true,
      newVersion: "v1.2.3",
    });

    const upgrade = useComputerUpgradeStore.getState().getUpgrade("daemon-1");
    expect(upgrade?.phase).toBe("completed");
    expect(upgrade?.error).toBeNull();
  });

  it("records failed done from WS event", () => {
    useComputerUpgradeStore.getState().recordDone({
      computer_id: "daemon-1",
      requestId: "req-1",
      ok: false,
      error: "Checksum mismatch",
    });

    const upgrade = useComputerUpgradeStore.getState().getUpgrade("daemon-1");
    expect(upgrade?.phase).toBe("failed");
    expect(upgrade?.error).toBe("Checksum mismatch");
  });

  it("clears and dismisses upgrades", () => {
    useComputerUpgradeStore.getState().recordDone({
      computer_id: "daemon-1",
      ok: true,
    });
    expect(useComputerUpgradeStore.getState().getUpgrade("daemon-1")).toBeDefined();

    useComputerUpgradeStore.getState().clearCompleted("daemon-1");
    expect(useComputerUpgradeStore.getState().getUpgrade("daemon-1")).toBeUndefined();

    useComputerUpgradeStore.getState().recordDone({
      computer_id: "daemon-2",
      ok: false,
    });
    expect(useComputerUpgradeStore.getState().getUpgrade("daemon-2")).toBeDefined();

    useComputerUpgradeStore.getState().dismissUpgrade("daemon-2");
    expect(useComputerUpgradeStore.getState().getUpgrade("daemon-2")).toBeUndefined();
  });
});
