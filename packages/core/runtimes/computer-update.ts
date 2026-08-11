import type { AgentRuntime } from "../types";
import {
  runtimeCanStartSelfUpdate,
  runtimeCurrentVersion,
  runtimeTargetVersion,
} from "./runtime-health-state";
import { isNewerCliVersion } from "./cli-version";

/** Same aggregation key as attentionMachineKey — one toast per daemon. */
export function computerUpdateMachineKey(runtime: AgentRuntime): string {
  const daemon = runtime.daemon_id?.trim();
  return daemon || `runtime:${runtime.id}`;
}

/**
 * One updatable computer (machine) ready for a sticky update toast.
 * Aggregated by {@link computerUpdateMachineKey} so multi-provider daemons
 * produce a single toast.
 */
export interface ComputerUpdateCandidate {
  machineKey: string;
  /** Daemon id required by POST /api/daemons/:id/upgrades. */
  daemonId: string;
  runtimeId: string;
  machineTitle: string;
  currentVersion: string | null;
  targetVersion: string;
}

const DISMISS_PREFIX = "multica:computer-update-dismiss:";

export function computerUpdateToastId(machineKey: string): string {
  return `computer-update:${machineKey}`;
}

export function computerUpdateDismissKey(
  workspaceId: string,
  machineKey: string,
): string {
  return `${DISMISS_PREFIX}${workspaceId}:${machineKey}`;
}

export function isComputerUpdateDismissed(
  storage: Pick<Storage, "getItem">,
  workspaceId: string,
  machineKey: string,
  targetVersion: string,
): boolean {
  return (
    storage.getItem(computerUpdateDismissKey(workspaceId, machineKey)) ===
    targetVersion
  );
}

export function dismissComputerUpdate(
  storage: Pick<Storage, "setItem">,
  workspaceId: string,
  machineKey: string,
  targetVersion: string,
): void {
  storage.setItem(
    computerUpdateDismissKey(workspaceId, machineKey),
    targetVersion,
  );
}

export function clearComputerUpdateDismiss(
  storage: Pick<Storage, "removeItem">,
  workspaceId: string,
  machineKey: string,
): void {
  storage.removeItem(computerUpdateDismissKey(workspaceId, machineKey));
}

export function machineTitleFromRuntime(runtime: AgentRuntime): string {
  const display = runtime.display_name?.trim();
  if (display) return display;
  const device = runtime.device_name?.trim();
  if (device) return device;
  const name = runtime.name?.trim();
  if (name) return name;
  const daemon = runtime.daemon_id?.trim();
  if (daemon) return daemon;
  return runtime.id;
}

/**
 * A terminal legacy request such as `latest` stops owning presentation once
 * the server can offer a newer exact release. This keeps the detail CTA and
 * the global update toast on the same target-selection contract.
 */
export function isMachineUpgradeFailureSuperseded(
  machineUpgrade: AgentRuntime["machine_upgrade"],
  currentVersion: string | null,
  daemonTargetVersion: string | null | undefined,
): boolean {
  const phase = machineUpgrade?.phase;
  const isTerminalFailure =
    phase === "failed" ||
    phase === "rolled_back" ||
    phase === "cancelled" ||
    phase === "timeout";
  const recordedTarget =
    machineUpgrade?.resolved_target?.trim() ||
    machineUpgrade?.requested_target?.trim() ||
    null;
  const daemonTarget = daemonTargetVersion?.trim() || null;
  return (
    isTerminalFailure &&
    !!recordedTarget &&
    !!daemonTarget &&
    isNewerCliVersion(daemonTarget, currentVersion) &&
    !isNewerCliVersion(recordedTarget, currentVersion)
  );
}

/**
 * Prefer machine-upgrade resolution, then daemon target, then legacy
 * runtime target — same order as Computers detail upgrade UI.
 */
export function resolveComputerUpdateTarget(
  runtime: AgentRuntime,
): string | null {
  const fromUpgrade =
    runtime.machine_upgrade?.resolved_target?.trim() ||
    runtime.machine_upgrade?.requested_target?.trim() ||
    null;
  const daemonTarget = runtime.daemon_target_version?.trim();
  const isSupersededFailure = isMachineUpgradeFailureSuperseded(
    runtime.machine_upgrade,
    runtimeCurrentVersion(runtime),
    daemonTarget,
  );
  if (fromUpgrade && !isSupersededFailure) return fromUpgrade;
  if (daemonTarget) return daemonTarget;
  return runtimeTargetVersion(runtime);
}

/**
 * Machines owned by `userId` that can start a self-update right now.
 * One row per machine; first eligible runtime is the initiate target.
 */
export function listComputerUpdateCandidates(
  runtimes: readonly AgentRuntime[] | null | undefined,
  userId: string | null | undefined,
  now: number = Date.now(),
): ComputerUpdateCandidate[] {
  if (!runtimes || !userId) return [];
  const seen = new Set<string>();
  const out: ComputerUpdateCandidate[] = [];
  for (const runtime of runtimes) {
    if (!runtimeCanStartSelfUpdate(runtime, userId, now)) continue;
    const daemonId = runtime.daemon_id?.trim();
    if (!daemonId) continue;
    const machineKey = computerUpdateMachineKey(runtime);
    if (seen.has(machineKey)) continue;
    // Prefer the computer-level target resolution even when
    // runtimeCanStartSelfUpdate only checked legacy target_version.
    const targetVersion =
      resolveComputerUpdateTarget(runtime) ?? runtimeTargetVersion(runtime);
    if (!targetVersion) continue;
    seen.add(machineKey);
    out.push({
      machineKey,
      daemonId,
      runtimeId: runtime.id,
      machineTitle: machineTitleFromRuntime(runtime),
      currentVersion: runtimeCurrentVersion(runtime),
      targetVersion,
    });
  }
  return out;
}
