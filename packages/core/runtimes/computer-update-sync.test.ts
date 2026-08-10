// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ComputerUpdateCandidate } from "./computer-update";
import {
  computerUpdateCandidatesFingerprint,
  computerUpdateToastContentKey,
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
