import type { AgentRuntime } from "../types";
import type { ComputerUpdateCandidate } from "./computer-update";
import type { ComputerUpgradeRecord } from "./computer-upgrade-store";

/**
 * Stable fingerprint of the updatable-machine set. Used to skip toast
 * re-sync when runtime list identity churns but eligibility did not change.
 */
export function computerUpdateCandidatesFingerprint(
  candidates: readonly ComputerUpdateCandidate[],
): string {
  if (candidates.length === 0) return "";
  // Sort so order churn in the runtime list does not re-fire sync.
  const rows = candidates.map(
    (c) =>
      `${c.machineKey}\0${c.daemonId}\0${c.runtimeId}\0${c.machineTitle}\0${c.currentVersion ?? ""}\0${c.targetVersion}`,
  );
  rows.sort();
  return rows.join("\n");
}

/**
 * Content signature for one toast surface. Equal signatures mean sonner
 * already shows the same UI — skip toast.custom.
 */
export function computerUpdateToastContentKey(parts: {
  phase: string;
  title: string;
  versionLine?: string | null;
  progressLabel?: string | null;
  errorLabel?: string | null;
  updateLabel: string;
  laterLabel: string;
  retryLabel: string;
  dismissLabel: string;
  busy: boolean;
  /** Target used by Later/dismiss — handlers bind this. */
  laterTarget?: string | null;
  /** Runtime id used by Update/Retry — handlers bind this. */
  actionRuntimeId?: string | null;
  actionDaemonId?: string | null;
}): string {
  return [
    parts.phase,
    parts.title,
    parts.versionLine ?? "",
    parts.progressLabel ?? "",
    parts.errorLabel ?? "",
    parts.updateLabel,
    parts.laterLabel,
    parts.retryLabel,
    parts.dismissLabel,
    parts.busy ? "1" : "0",
    parts.laterTarget ?? "",
    parts.actionRuntimeId ?? "",
    parts.actionDaemonId ?? "",
  ].join("\0");
}

/** Upgrade phases that still hold a sticky "Updating…" toast on screen. */
function isInFlight(upgrade: ComputerUpgradeRecord): boolean {
  return upgrade.phase === "pending" || upgrade.phase === "running";
}

/**
 * Runtime row that reports the machine version for an in-flight upgrade.
 * Daemon id is the real key; runtime id and machine key only cover records
 * rebuilt from a progress event, which carries neither.
 */
export function findComputerUpgradeRuntime(
  runtimes: readonly AgentRuntime[] | null | undefined,
  upgrade: Pick<ComputerUpgradeRecord, "daemonId" | "runtimeId" | "machineKey">,
): AgentRuntime | undefined {
  return runtimes?.find(
    (runtime) =>
      (!!runtime.daemon_id && runtime.daemon_id === upgrade.daemonId) ||
      runtime.id === upgrade.runtimeId ||
      runtime.name === upgrade.machineKey,
  );
}

/** Compares release tags ignoring the optional leading `v` on either side. */
export function computerVersionsMatch(
  left: string | null | undefined,
  right: string | null | undefined,
): boolean {
  const normalize = (value: string | null | undefined) =>
    (value ?? "").trim().replace(/^v/, "");
  const a = normalize(left);
  return a !== "" && a === normalize(right);
}

/**
 * Fingerprint of the version each in-flight upgrade's machine reports.
 *
 * A restart handoff never emits `computer:upgrade:done` (the old binary exits
 * after the "restarting" progress event and the successor only reconciles its
 * journal), so the successor re-registering on the target version is the only
 * signal that retires the toast. The listener holds the runtime list in a ref
 * to ignore identity churn, and by then the machine has long left the candidate
 * list — this fingerprint is what tells the sync effect to look again.
 */
export function computerUpgradeVersionsFingerprint(
  runtimes: readonly AgentRuntime[] | null | undefined,
  upgrades: Record<string, ComputerUpgradeRecord>,
): string {
  const rows: string[] = [];
  for (const upgrade of Object.values(upgrades)) {
    if (!isInFlight(upgrade)) continue;
    const runtime = findComputerUpgradeRuntime(runtimes, upgrade);
    rows.push(`${upgrade.daemonId}\0${runtime?.current_version ?? ""}`);
  }
  if (rows.length === 0) return "";
  rows.sort();
  return rows.join("\n");
}
