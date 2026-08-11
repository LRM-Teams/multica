// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "../types";
import {
  clearComputerUpdateDismiss,
  computerUpdateDismissKey,
  computerUpdateToastId,
  dismissComputerUpdate,
  isComputerUpdateDismissed,
  listComputerUpdateCandidates,
  machineTitleFromRuntime,
} from "./computer-update";

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: "0.3.0",
    target_version: "0.4.0",
    update_state: "idle",
    runtime_health: "update_available",
    owner_id: "user-1",
    last_seen_at: new Date().toISOString(),
    created_at: "2026-07-03T00:00:00Z",
    updated_at: "2026-07-03T00:00:00Z",
    ...overrides,
  } as AgentRuntime;
}

describe("listComputerUpdateCandidates", () => {
  it("returns one candidate per owned updatable machine", () => {
    const list = listComputerUpdateCandidates(
      [
        makeRuntime({ id: "rt-a", daemon_id: "d-1", device_name: "MacBook" }),
        makeRuntime({
          id: "rt-b",
          daemon_id: "d-1",
          provider: "cursor",
          device_name: "MacBook",
        }),
        makeRuntime({
          id: "rt-c",
          daemon_id: "d-2",
          device_name: "Office PC",
          target_version: "0.5.0",
        }),
      ],
      "user-1",
    );
    expect(list).toEqual([
      {
        machineKey: "d-1",
        daemonId: "d-1",
        runtimeId: "rt-a",
        machineTitle: "MacBook",
        currentVersion: "0.3.0",
        targetVersion: "0.4.0",
      },
      {
        machineKey: "d-2",
        daemonId: "d-2",
        runtimeId: "rt-c",
        machineTitle: "Office PC",
        currentVersion: "0.3.0",
        targetVersion: "0.5.0",
      },
    ]);
  });

  it("skips other owners, sandbox, desktop-managed, and mid-lifecycle", () => {
    const list = listComputerUpdateCandidates(
      [
        makeRuntime({ id: "other", owner_id: "u2", daemon_id: "d-other" }),
        makeRuntime({
          id: "sandbox",
          daemon_id: "d-sb",
          metadata: { sandbox_instance_id: "sb-1" },
        }),
        makeRuntime({
          id: "desktop",
          daemon_id: "d-desk",
          metadata: { launched_by: "desktop" },
        }),
        makeRuntime({
          id: "staged",
          daemon_id: "d-staged",
          update_state: "ready_to_apply",
        }),
      ],
      "user-1",
    );
    expect(list).toEqual([]);
  });

  it("uses the exact daemon target after a legacy latest request failed", () => {
    const list = listComputerUpdateCandidates(
      [
        makeRuntime({
          current_version: "0.4.24-alpha.9",
          target_version: "v0.4.24-alpha.11",
          daemon_target_version: "v0.4.24-alpha.11",
          machine_upgrade: {
            id: "machine-upgrade-failed-latest",
            daemon_id: "daemon-1",
            request_id: "request-failed-latest",
            requested_target: "latest",
            phase: "failed",
            error_code: "target_resolution_failed",
            error_message: "resolve latest machine upgrade target: context deadline exceeded",
            created_at: "2026-08-11T09:58:31Z",
            updated_at: "2026-08-11T09:58:46Z",
          },
        }),
      ],
      "user-1",
    );

    expect(list).toHaveLength(1);
    expect(list[0]?.targetVersion).toBe("v0.4.24-alpha.11");
  });

  it("uses the newer daemon target after a completed upgrade caught up", () => {
    const list = listComputerUpdateCandidates(
      [
        makeRuntime({
          id: "0ad7ac57-7c84-45b4-a69c-d6591116230b",
          daemon_id: "1298b34b-b7de-4309-bdfb-71043265052d",
          display_name: "s143",
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
        }),
      ],
      "user-1",
    );

    expect(list).toHaveLength(1);
    expect(list[0]?.targetVersion).toBe("v0.4.24-alpha.12");
  });
});

describe("machineTitleFromRuntime", () => {
  it("prefers device_name over name", () => {
    expect(
      machineTitleFromRuntime(
        makeRuntime({ device_name: "Studio", name: "hostname" }),
      ),
    ).toBe("Studio");
  });
});

describe("dismiss storage helpers", () => {
  it("keys toast ids and dismiss entries stably", () => {
    expect(computerUpdateToastId("d-1")).toBe("computer-update:d-1");
    expect(computerUpdateDismissKey("ws-1", "d-1")).toBe(
      "multica:computer-update-dismiss:ws-1:d-1",
    );
  });

  it("treats matching targetVersion as dismissed; new target reopens", () => {
    const store = new Map<string, string>();
    const storage = {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => {
        store.set(k, v);
      },
      removeItem: (k: string) => {
        store.delete(k);
      },
    };

    expect(isComputerUpdateDismissed(storage, "ws-1", "d-1", "0.4.0")).toBe(
      false,
    );
    dismissComputerUpdate(storage, "ws-1", "d-1", "0.4.0");
    expect(isComputerUpdateDismissed(storage, "ws-1", "d-1", "0.4.0")).toBe(
      true,
    );
    expect(isComputerUpdateDismissed(storage, "ws-1", "d-1", "0.5.0")).toBe(
      false,
    );
    clearComputerUpdateDismiss(storage, "ws-1", "d-1");
    expect(isComputerUpdateDismissed(storage, "ws-1", "d-1", "0.4.0")).toBe(
      false,
    );
  });
});
