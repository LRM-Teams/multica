// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "../types";
import type { ComputerUpdateCandidate } from "./computer-update";
import type { ComputerUpgradeRecord } from "./computer-upgrade-store";
import {
  computerUpdateCandidatesFingerprint,
  computerUpdateToastContentKey,
  computerUpgradeVersionsFingerprint,
  computerVersionsMatch,
  findComputerUpgradeRuntime,
} from "./computer-update-sync";

const base: ComputerUpdateCandidate = {
  machineKey: "d-1",
  daemonId: "d-1",
  runtimeId: "rt-1",
  machineTitle: "Mac",
  currentVersion: "0.3.0",
  targetVersion: "0.4.0",
};

describe("computerUpdateCandidatesFingerprint", () => {
  it("is order-independent", () => {
    const a: ComputerUpdateCandidate[] = [
      base,
      { ...base, machineKey: "d-2", daemonId: "d-2", runtimeId: "rt-2" },
    ];
    const b = [...a].reverse();
    expect(computerUpdateCandidatesFingerprint(a)).toBe(
      computerUpdateCandidatesFingerprint(b),
    );
  });

  it("changes when target version changes", () => {
    const before = computerUpdateCandidatesFingerprint([base]);
    const after = computerUpdateCandidatesFingerprint([
      { ...base, targetVersion: "0.5.0" },
    ]);
    expect(before).not.toBe(after);
  });

  it("is empty for no candidates", () => {
    expect(computerUpdateCandidatesFingerprint([])).toBe("");
  });
});

describe("computerUpdateToastContentKey", () => {
  it("matches for identical content", () => {
    const a = computerUpdateToastContentKey({
      phase: "prompt",
      title: "Update available for Mac",
      versionLine: "0.3 → 0.4",
      updateLabel: "Update",
      laterLabel: "Later",
      retryLabel: "Retry",
      dismissLabel: "Dismiss",
      busy: false,
      laterTarget: "0.4",
      actionRuntimeId: "rt-1",
      actionDaemonId: "d-1",
    });
    const b = computerUpdateToastContentKey({
      phase: "prompt",
      title: "Update available for Mac",
      versionLine: "0.3 → 0.4",
      updateLabel: "Update",
      laterLabel: "Later",
      retryLabel: "Retry",
      dismissLabel: "Dismiss",
      busy: false,
      laterTarget: "0.4",
      actionRuntimeId: "rt-1",
      actionDaemonId: "d-1",
    });
    expect(a).toBe(b);
  });

  it("differs when progress label changes", () => {
    const pending = computerUpdateToastContentKey({
      phase: "updating",
      title: "Updating Mac…",
      progressLabel: "Starting…",
      updateLabel: "Update",
      laterLabel: "Later",
      retryLabel: "Retry",
      dismissLabel: "Dismiss",
      busy: true,
    });
    const running = computerUpdateToastContentKey({
      phase: "updating",
      title: "Updating Mac…",
      progressLabel: "Downloading…",
      updateLabel: "Update",
      laterLabel: "Later",
      retryLabel: "Retry",
      dismissLabel: "Dismiss",
      busy: true,
    });
    expect(pending).not.toBe(running);
  });
});

const upgrade: ComputerUpgradeRecord = {
  daemonId: "d-1",
  machineKey: "d-1",
  runtimeId: "rt-1",
  machineTitle: "Mac",
  targetVersion: "v0.4.0",
  requestId: "req-1",
  phase: "running",
  startedAt: 0,
};

function runtime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "d-1",
    name: "mac",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: "0.3.0",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-1",
    last_seen_at: null,
    created_at: "2026-08-20T00:00:00Z",
    updated_at: "2026-08-20T00:00:00Z",
    ...overrides,
  } as AgentRuntime;
}

describe("computerVersionsMatch", () => {
  it("ignores the leading v on either side", () => {
    expect(computerVersionsMatch("0.4.0", "v0.4.0")).toBe(true);
    expect(computerVersionsMatch("v0.4.0", "0.4.0")).toBe(true);
  });

  it("is false for a different version", () => {
    expect(computerVersionsMatch("0.3.0", "v0.4.0")).toBe(false);
  });

  it("is false when either side is missing", () => {
    expect(computerVersionsMatch(null, "v0.4.0")).toBe(false);
    expect(computerVersionsMatch("0.4.0", "")).toBe(false);
  });
});

describe("findComputerUpgradeRuntime", () => {
  it("matches on daemon id first", () => {
    const other = runtime({ id: "rt-2", daemon_id: "d-2", name: "other" });
    const found = findComputerUpgradeRuntime([other, runtime()], upgrade);
    expect(found?.daemon_id).toBe("d-1");
  });

  it("falls back to runtime id and machine key for progress-built records", () => {
    const rebuilt = { ...upgrade, daemonId: "unknown", runtimeId: "rt-1" };
    expect(
      findComputerUpgradeRuntime([runtime({ daemon_id: undefined })], rebuilt)
        ?.id,
    ).toBe("rt-1");
  });

  it("is undefined when the machine is absent", () => {
    expect(findComputerUpgradeRuntime([], upgrade)).toBeUndefined();
  });
});

describe("computerUpgradeVersionsFingerprint", () => {
  it("changes when an upgrading machine reports the new version", () => {
    const upgrades = { "d-1": upgrade };
    const before = computerUpgradeVersionsFingerprint([runtime()], upgrades);
    const after = computerUpgradeVersionsFingerprint(
      [runtime({ current_version: "0.4.0" })],
      upgrades,
    );
    expect(before).not.toBe(after);
  });

  it("ignores runtime list churn that leaves versions alone", () => {
    const upgrades = { "d-1": upgrade };
    expect(computerUpgradeVersionsFingerprint([runtime()], upgrades)).toBe(
      computerUpgradeVersionsFingerprint([runtime()], upgrades),
    );
  });

  it("tracks a machine that dropped off the runtime list while restarting", () => {
    const upgrades = { "d-1": upgrade };
    const offline = computerUpgradeVersionsFingerprint([], upgrades);
    expect(offline).not.toBe("");
    expect(offline).not.toBe(
      computerUpgradeVersionsFingerprint(
        [runtime({ current_version: "0.4.0" })],
        upgrades,
      ),
    );
  });

  it("is empty once no upgrade is in flight", () => {
    expect(
      computerUpgradeVersionsFingerprint([runtime()], {
        "d-1": { ...upgrade, phase: "completed" },
      }),
    ).toBe("");
  });
});
