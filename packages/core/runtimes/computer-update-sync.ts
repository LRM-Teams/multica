import type { ComputerUpdateCandidate } from "./computer-update";

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
