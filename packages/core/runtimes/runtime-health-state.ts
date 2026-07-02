import type { AgentRuntime, RuntimeHealthState } from "../types";

// `awaiting_confirmation` is intentionally omitted: it is the quiet
// completed -> register confirmation window, not a fresh "needs update" prompt.
const ATTENTION_HEALTH_STATES = new Set<RuntimeHealthState>([
  "update_available",
  "updating",
  "failed",
  "offline",
]);

const HEALTH_PRIORITY: Record<RuntimeHealthState, number> = {
  ok: 0,
  awaiting_confirmation: 1,
  update_available: 2,
  updating: 3,
  offline: 4,
  failed: 5,
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
  return ATTENTION_HEALTH_STATES.has(runtimeHealthState(runtime));
}

export function runtimeCanStartSelfUpdate(
  runtime: AgentRuntime,
  userId: string | null | undefined,
): boolean {
  if (!isCurrentUserLocalRuntime(runtime, userId)) return false;
  if (isDesktopManagedRuntime(runtime)) return false;
  if (runtime.status !== "online") return false;
  return (
    runtimeHealthState(runtime) === "update_available" &&
    runtimeTargetVersion(runtime) !== null
  );
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
