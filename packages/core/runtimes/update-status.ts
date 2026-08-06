import type {
  RuntimeHealthState,
  RuntimeUpdateState,
  RuntimeUpdateStatus,
} from "../types";

/**
 * Display derivation for a runtime self-update, shared by every surface that
 * shows update progress (the detail-page UpdateSection and the AppShell update
 * prompt). This is a pure projection of the **server contract** — the runtime's
 * `update_state` / `runtime_health` plus an optional in-flight poll result — into
 * the `RuntimeUpdateStatus` vocabulary the UI renders. It deliberately does NOT
 * model the daemon's own state machine; it only decides what a consumer should
 * display from whatever the last runtime projection said.
 */

/**
 * Map the runtime's advertised `update_state` onto the render vocabulary.
 * `idle`/absent means "nothing to show"; `timed_out` normalizes to `timeout`.
 */
export function statusFromUpdateState(
  state: RuntimeUpdateState | undefined,
): RuntimeUpdateStatus | null {
  switch (state) {
    case "pending":
    case "running":
    case "completed":
    case "ready_to_apply":
    case "failed":
      return state;
    case "timed_out":
      return "timeout";
    case "idle":
    case undefined:
      return null;
  }
}

/**
 * The states at which an update is finished and polling must stop. `ready_to_apply`
 * is terminal: the new version is staged and applies when the runtime next goes
 * idle — there is nothing further to wait for. Treating it as non-terminal is the
 * "black window" bug (poll spins forever, no terminal UI, no runtime refresh).
 */
export const UPDATE_TERMINAL_STATUSES: ReadonlySet<RuntimeUpdateStatus> =
  new Set<RuntimeUpdateStatus>([
    "completed",
    "ready_to_apply",
    "failed",
    "timeout",
  ]);

export function isTerminalUpdateStatus(
  status: RuntimeUpdateStatus | null | undefined,
): boolean {
  return !!status && UPDATE_TERMINAL_STATUSES.has(status);
}

/**
 * `update_state` values that mean a self-update is genuinely underway or staged.
 * A new update must NOT be offered while the runtime is in one of these, even if
 * the backend still reports `runtime_health: "update_available"` for a staged
 * (`ready_to_apply`) daemon — otherwise the AppShell prompt re-opens on a staged
 * runtime and pins a terminal modal.
 *
 * `completed` is deliberately NOT here: a terminal `completed` row lingers (~6h),
 * and if a newer version releases during that window the server projects
 * `update_available + completed`, which must stay eligible so consecutive
 * upgrades are not blocked by stale terminal history. `failed`/`timed_out` are
 * handled by the existing failed/retry surface, not the AppShell auto-prompt.
 */
export const ACTIVE_UPDATE_LIFECYCLE_STATES: ReadonlySet<RuntimeUpdateState> =
  new Set<RuntimeUpdateState>(["pending", "running", "ready_to_apply"]);

export function isUpdateLifecycleActive(
  state: RuntimeUpdateState | undefined,
): boolean {
  return !!state && ACTIVE_UPDATE_LIFECYCLE_STATES.has(state);
}

export interface DeriveUpdateStatusInput {
  /** Status from an in-flight machine-upgrade poll, if one is running. */
  pollStatus?: RuntimeUpdateStatus | null;
  /** The runtime projection's `update_state` (post-refresh source of truth). */
  updateState?: RuntimeUpdateState;
  /** The runtime projection's `runtime_health`. */
  runtimeHealth?: RuntimeHealthState;
}

/**
 * Decide the single status a surface should display. An active poll wins (it is
 * the freshest signal); otherwise derive from the runtime projection so a daemon
 * that is already downloading/staged shows the right state without waiting for a
 * first poll tick — the crux of "I clicked update and nothing happened".
 */
export function deriveUpdateStatus({
  pollStatus,
  updateState,
  runtimeHealth = "ok",
}: DeriveUpdateStatusInput): RuntimeUpdateStatus | null {
  if (pollStatus) return pollStatus;

  const contractStatus = statusFromUpdateState(updateState);
  if (contractStatus === "ready_to_apply") return "ready_to_apply";
  if (runtimeHealth === "updating") {
    return contractStatus === "pending" ? "pending" : "running";
  }
  if (runtimeHealth === "failed") {
    return contractStatus === "timeout" ? "timeout" : "failed";
  }
  return null;
}
