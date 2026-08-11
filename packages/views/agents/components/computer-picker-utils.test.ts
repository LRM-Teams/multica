import { describe, expect, it } from "vitest";
import type { RuntimeMachine } from "../../runtimes/components/runtime-machines";
import {
  firstOnlineRuntimeIdOnMachine,
  runtimeBelongsToMachine,
} from "./computer-picker-utils";

function machine(
  runtimes: Array<{ id: string; status: string; last_seen_at?: string | null }>,
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
    const now = Date.now();
    const m = machine([
      { id: "rt-offline", status: "offline", last_seen_at: null },
      { id: "rt-online", status: "online", last_seen_at: new Date(now).toISOString() },
    ]);
    expect(firstOnlineRuntimeIdOnMachine(m, now)).toBe("rt-online");
  });

  it("runtimeBelongsToMachine checks membership", () => {
    const m = machine([{ id: "rt-1", status: "online" }]);
    expect(runtimeBelongsToMachine("rt-1", m)).toBe(true);
    expect(runtimeBelongsToMachine("rt-other", m)).toBe(false);
    expect(runtimeBelongsToMachine("rt-1", null)).toBe(false);
  });
});
