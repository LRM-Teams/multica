import { describe, it, expect } from "vitest";
import type {
  RuntimeHealthState,
  RuntimeUpdateState,
  RuntimeUpdateStatus,
} from "../types";
import {
  statusFromUpdateState,
  isTerminalUpdateStatus,
  UPDATE_TERMINAL_STATUSES,
  isUpdateLifecycleActive,
  deriveUpdateStatus,
} from "./update-status";

describe("statusFromUpdateState", () => {
  const cases: Array<[RuntimeUpdateState | undefined, RuntimeUpdateStatus | null]> = [
    ["idle", null],
    [undefined, null],
    ["pending", "pending"],
    ["running", "running"],
    ["completed", "completed"],
    ["ready_to_apply", "ready_to_apply"],
    ["failed", "failed"],
    ["timed_out", "timeout"],
  ];
  it.each(cases)("maps %s -> %s", (state, expected) => {
    expect(statusFromUpdateState(state)).toBe(expected);
  });
});

describe("isTerminalUpdateStatus / UPDATE_TERMINAL_STATUSES", () => {
  it("treats ready_to_apply as terminal (the black-window fix)", () => {
    expect(isTerminalUpdateStatus("ready_to_apply")).toBe(true);
    expect(UPDATE_TERMINAL_STATUSES.has("ready_to_apply")).toBe(true);
  });

  it("terminates on completed/failed/timeout too", () => {
    expect(isTerminalUpdateStatus("completed")).toBe(true);
    expect(isTerminalUpdateStatus("failed")).toBe(true);
    expect(isTerminalUpdateStatus("timeout")).toBe(true);
  });

  it("keeps polling through non-terminal states", () => {
    expect(isTerminalUpdateStatus("pending")).toBe(false);
    expect(isTerminalUpdateStatus("running")).toBe(false);
    expect(isTerminalUpdateStatus(null)).toBe(false);
    expect(isTerminalUpdateStatus(undefined)).toBe(false);
  });
});

describe("isUpdateLifecycleActive", () => {
  it("is active while an update is underway or staged (blocks a new update)", () => {
    expect(isUpdateLifecycleActive("pending")).toBe(true);
    expect(isUpdateLifecycleActive("running")).toBe(true);
    expect(isUpdateLifecycleActive("ready_to_apply")).toBe(true);
  });

  it("is NOT active for idle/completed — a newer release must stay startable", () => {
    expect(isUpdateLifecycleActive("idle")).toBe(false);
    expect(isUpdateLifecycleActive("completed")).toBe(false);
    expect(isUpdateLifecycleActive("failed")).toBe(false);
    expect(isUpdateLifecycleActive("timed_out")).toBe(false);
    expect(isUpdateLifecycleActive(undefined)).toBe(false);
  });
});

describe("deriveUpdateStatus", () => {
  it("prefers an in-flight poll status over the projection", () => {
    expect(
      deriveUpdateStatus({
        pollStatus: "running",
        updateState: "ready_to_apply",
        runtimeHealth: "updating",
      }),
    ).toBe("running");
  });

  it("shows ready_to_apply from the projection with no poll (staged daemon)", () => {
    expect(
      deriveUpdateStatus({ updateState: "ready_to_apply", runtimeHealth: "ok" }),
    ).toBe("ready_to_apply");
  });

  it("shows the downloading/installing intermediate when health is updating", () => {
    expect(
      deriveUpdateStatus({ updateState: "pending", runtimeHealth: "updating" }),
    ).toBe("pending");
    expect(
      deriveUpdateStatus({ updateState: "running", runtimeHealth: "updating" }),
    ).toBe("running");
    // updating health with no specific update_state still reads as in-progress.
    expect(deriveUpdateStatus({ runtimeHealth: "updating" })).toBe("running");
  });

  it("maps failed health, normalizing timed_out to timeout", () => {
    expect(
      deriveUpdateStatus({ updateState: "timed_out", runtimeHealth: "failed" }),
    ).toBe("timeout");
    expect(
      deriveUpdateStatus({ updateState: "failed", runtimeHealth: "failed" }),
    ).toBe("failed");
    expect(deriveUpdateStatus({ runtimeHealth: "failed" })).toBe("failed");
  });

  it("shows nothing for a healthy idle runtime", () => {
    expect(deriveUpdateStatus({ updateState: "idle", runtimeHealth: "ok" })).toBeNull();
    expect(deriveUpdateStatus({ runtimeHealth: "ok" })).toBeNull();
    expect(deriveUpdateStatus({})).toBeNull();
    // update_available is an actionable state, not an in-progress status.
    expect(
      deriveUpdateStatus({ updateState: "idle", runtimeHealth: "update_available" }),
    ).toBeNull();
  });

  it("defaults absent runtimeHealth to ok", () => {
    const health: RuntimeHealthState | undefined = undefined;
    expect(deriveUpdateStatus({ updateState: "running", runtimeHealth: health })).toBeNull();
  });
});
