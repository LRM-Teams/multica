import type { AgentRuntime, RuntimeHealthState } from "../types";
import { deriveRuntimeHealth } from "./derive-health";
import { isUpdateLifecycleActive } from "./update-status";

/**
 * How a runtime's update lifecycle should be *presented* on health badges/labels.
 * Extends {@link RuntimeHealthState} with `ready_to_apply`, which is an
 * `update_state` the backend collapses into `runtime_health: "update_available"`.
 * Surfaces must derive this (via {@link deriveRuntimeHealthPresentation}) rather
 * than read `runtime_health` directly, so a staged daemon reads "downloaded,
 * applies when idle" instead of the generic "update available".
 */
export type RuntimeHealthPresentation = RuntimeHealthState | "ready_to_apply";

const ATTENTION_HEALTH_STATES = new Set<RuntimeHealthState>([
  "update_available",
  "updating",
  "failed",
  "offline",
]);

const HEALTH_PRIORITY: Record<RuntimeHealthState, number> = {
  ok: 0,
  update_available: 1,
  updating: 2,
  offline: 3,
  failed: 4,
};

export function runtimeHealthState(
  runtime: AgentRuntime,
): RuntimeHealthState {
  return runtime.runtime_health ?? "ok";
}

export function runtimeCurrentVersion(runtime: AgentRuntime): string | null {
  const version = runtime.current_version;
  return typeof version === "string" && version.trim() ? version.trim() : null;
}

export function runtimeTargetVersion(runtime: AgentRuntime): string | null {
  const version = runtime.target_version;
  return typeof version === "string" && version.trim() ? version.trim() : null;
}

export function runtimeLaunchedBy(runtime: AgentRuntime): string | null {
  const launchedBy = runtime.metadata?.launched_by;
  return typeof launchedBy === "string" && launchedBy ? launchedBy : null;
}

/**
 * Whether this runtime backs a daemon-enabled env-dispatch sandbox. The daemon
 * forwards `MULTICA_SANDBOX_INSTANCE_ID` on registration, which the server
 * records as `metadata.sandbox_instance_id` (see server daemon handler).
 * Sandbox daemons are managed by the sandbox runtime, not the user's desktop,
 * so a stale CLI on a sandbox must not drive the "your daemon needs an
 * upgrade" popup or sidebar attention — only non-sandbox daemons prompt.
 */
export function isSandboxRuntime(runtime: AgentRuntime): boolean {
  const sid = runtime.metadata?.sandbox_instance_id;
  return typeof sid === "string" && sid.trim() !== "";
}

export function isDesktopManagedRuntime(runtime: AgentRuntime): boolean {
  return runtimeLaunchedBy(runtime) === "desktop";
}

export function isCurrentUserLocalRuntime(
  runtime: AgentRuntime,
  userId: string | null | undefined,
): boolean {
  return (
    runtime.runtime_mode === "local" &&
    !!userId &&
    runtime.owner_id === userId
  );
}

export function runtimeHasHealthAttention(
  runtime: AgentRuntime,
  userId: string | null | undefined,
): boolean {
  if (!isCurrentUserLocalRuntime(runtime, userId)) return false;
  if (isDesktopManagedRuntime(runtime)) return false;
  // Sandbox daemons are runtime-managed; their CLI expiry is handled by the
  // sandbox runtime, so they must not surface an upgrade prompt to the user.
  if (isSandboxRuntime(runtime)) return false;
  return ATTENTION_HEALTH_STATES.has(runtimeHealthState(runtime));
}

export function runtimeCanStartSelfUpdate(
  runtime: AgentRuntime,
  userId: string | null | undefined,
  now: number = Date.now(),
): boolean {
  if (!isCurrentUserLocalRuntime(runtime, userId)) return false;
  if (isDesktopManagedRuntime(runtime)) return false;
  // Sandbox daemons are managed by the sandbox runtime; the user can't
  // self-update them, so don't offer the start-update action.
  if (isSandboxRuntime(runtime)) return false;
  // Read online-ness through the derived, staleness-aware health instead of
  // the raw `status` column: a heartbeat that silently stopped can leave
  // `status: "online"` long after the daemon is actually gone (#10 —
  // "runtime online status" had two divergent sources across the app).
  if (deriveRuntimeHealth(runtime, now) !== "online") return false;
  // Don't offer a new update while one is genuinely underway or staged. The
  // backend keeps `runtime_health: "update_available"` through `ready_to_apply`,
  // so health alone would re-open the prompt on a staged daemon (pinning a
  // terminal modal). `completed` stays eligible so a newer release during the
  // terminal window is not blocked.
  if (isUpdateLifecycleActive(runtime.update_state)) return false;
  return (
    runtimeHealthState(runtime) === "update_available" &&
    runtimeTargetVersion(runtime) !== null
  );
}

/**
 * Presentation state for health badges/labels: `ready_to_apply` (staged) and
 * in-progress `update_state`s override the coarse `runtime_health` the backend
 * reports, so all runtime surfaces show the same three-phase lifecycle from one
 * source. `completed`/`idle`/`failed`/`offline` fall through to `runtime_health`
 * (e.g. `completed + update_available` reads as "update available" — a newer
 * release the user can start).
 */
export function deriveRuntimeHealthPresentation(
  runtime: AgentRuntime,
): RuntimeHealthPresentation {
  const health = runtimeHealthState(runtime);
  // Offline wins (fail-closed), mirroring the server's offline-first precedence:
  // a disconnected daemon cannot actually be mid-download or staged regardless of
  // the last-seen `update_state`, so we must not paint it "Updating"/"Ready".
  if (health === "offline") return "offline";
  const state = runtime.update_state;
  if (state === "ready_to_apply") return "ready_to_apply";
  if (state === "pending" || state === "running") return "updating";
  return health;
}

const PRESENTATION_PRIORITY: Record<RuntimeHealthPresentation, number> = {
  ok: 0,
  update_available: 1,
  ready_to_apply: 2,
  updating: 3,
  offline: 4,
  failed: 5,
};

/**
 * Aggregate the highest-severity {@link RuntimeHealthPresentation} across a
 * machine's runtimes, so a machine header agrees with its rows (a staged runtime
 * reads "ready to apply" at both levels instead of the header showing the raw
 * "update available"). Offline/failed still dominate progress states.
 */
export function aggregateRuntimeHealthPresentation(
  runtimes: AgentRuntime[],
): RuntimeHealthPresentation | null {
  let selected: RuntimeHealthPresentation | null = null;
  for (const runtime of runtimes) {
    const presentation = deriveRuntimeHealthPresentation(runtime);
    if (
      !selected ||
      PRESENTATION_PRIORITY[presentation] > PRESENTATION_PRIORITY[selected]
    ) {
      selected = presentation;
    }
  }
  return selected;
}

export function aggregateRuntimeHealthState(
  runtimes: AgentRuntime[],
): RuntimeHealthState | null {
  let selected: RuntimeHealthState | null = null;
  for (const runtime of runtimes) {
    const health = runtimeHealthState(runtime);
    if (!selected || HEALTH_PRIORITY[health] > HEALTH_PRIORITY[selected]) {
      selected = health;
    }
  }
  return selected;
}
