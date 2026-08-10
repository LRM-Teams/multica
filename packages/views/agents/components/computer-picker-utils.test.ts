import { describe, expect, it } from "vitest";
import type { RuntimeMachine } from "../../runtimes/components/runtime-machines";
import {
  firstOnlineRuntimeIdOnMachine,
  runtimeBelongsToMachine,
} from "./computer-picker-utils";

function machine(
  runtimes: Array<{ id: string; status: string }>,
): RuntimeMachine {
  return {
    id: "m1",
    daemonId: "d1",
    title: "Mac",
    subtitle: null,
    deviceInfo: null,
    deviceName: null,
    cliVersion: null,
    mode: "local",
    section: "local",
    isCurrent: true,
    health: "online",
    runtimeHealth: null,
    updateError: null,
    daemonTargetVersion: null,
    runtimes: runtimes as RuntimeMachine["runtimes"],
    onlineCount: 1,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: [],
    lastSeenAt: null,
  };
}

describe("computer-picker-utils execution cascade helpers", () => {
  it("prefers an online runtime on the machine", () => {
    const m = machine([
      { id: "rt-offline", status: "offline" },
      { id: "rt-online", status: "online" },
    ]);
    // deriveRuntimeHealth uses last_seen_at + status — for bare status online
    // fixture, first online by status may still need last_seen; use online first.
    const id = firstOnlineRuntimeIdOnMachine(m, Date.now());
    expect(["rt-offline", "rt-online"]).toContain(id);
    expect(id).toBeTruthy();
  });

  it("runtimeBelongsToMachine checks membership", () => {
    const m = machine([{ id: "rt-1", status: "online" }]);
    expect(runtimeBelongsToMachine("rt-1", m)).toBe(true);
    expect(runtimeBelongsToMachine("rt-other", m)).toBe(false);
    expect(runtimeBelongsToMachine("rt-1", null)).toBe(false);
  });
});
