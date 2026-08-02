import type {
  AgentLifecycleActionKind,
  AgentLifecycleActionState,
  AgentLifecycleOperationStatus,
  AgentLifecyclePreflight,
} from "../types";

/**
 * Pure helpers for the agent lifecycle actions (#632/#633). Kept UI-free so the
 * terminal-set (poll-stop) discipline and the per-action preflight accessor are
 * unit-testable without React — mirrors `runtimes/update-status.ts` (#687).
 */

/** Statuses at which a lifecycle operation is finished and polling must stop. */
export const AGENT_LIFECYCLE_TERMINAL_STATUSES: ReadonlySet<AgentLifecycleOperationStatus> =
  new Set<AgentLifecycleOperationStatus>(["succeeded", "failed"]);

export function isTerminalAgentLifecycleStatus(
  status: AgentLifecycleOperationStatus | null | undefined,
): boolean {
  return !!status && AGENT_LIFECYCLE_TERMINAL_STATUSES.has(status);
}

/**
 * The server-authoritative executability for one tier. Returns a hard-disabled
 * fallback when the preflight is missing or omits the action, so the UI fails
 * closed (disabled) rather than offering an action the server hasn't blessed —
 * this also covers the dormant `unsupported_runtime_capability` window before
 * #677 D6 advertises the capability.
 */
export function agentLifecycleActionState(
  preflight: AgentLifecyclePreflight | undefined | null,
  kind: AgentLifecycleActionKind,
): AgentLifecycleActionState {
  const state = preflight?.actions?.[kind];
  if (!state) {
    return { supported: false, disabled_reason: "unavailable", execution_mode: "immediate" };
  }
  return state;
}

/** Whether a tier can be started right now (idle) vs. only scheduled after the run. */
export function isImmediateExecution(state: AgentLifecycleActionState): boolean {
  return state.execution_mode === "immediate";
}

/**
 * `disabled_reason` values the FE has copy for (`agents.json`'s
 * `restart_modal.disabled_reason`). Shared between the restart modal's
 * per-tier list and the trigger button's standing reason line so both read
 * the same server field through the same key set — never re-derive this
 * from `agent.status`.
 */
export const KNOWN_LIFECYCLE_DISABLED_REASONS = new Set([
  "agent_active",
  // The `unsupported_runtime_capability` copy (agents.json's
  // restart_modal.disabled_reason) names a specific daemon version
  // (currently v0.3.95) — that's the daemon build that first advertises
  // server/pkg/protocol/messages.go's DaemonCapabilityAgentLifecycleActions.
  // If that minimum version ever moves, the locale copy needs to move with
  // it; there's no code-level link between them to catch drift.
  "unsupported_runtime_capability",
  "no_runtime",
  "offline",
  "no_permission",
]);

export type LifecycleDisabledReasonKey =
  | "agent_active"
  | "unsupported_runtime_capability"
  | "no_runtime"
  | "offline"
  | "no_permission"
  | "unavailable";

/** Maps a server `disabled_reason` to a known copy key, falling back to "unavailable". */
export function resolveLifecycleDisabledReasonKey(
  reason: string | null | undefined,
): LifecycleDisabledReasonKey {
  return reason && KNOWN_LIFECYCLE_DISABLED_REASONS.has(reason)
    ? (reason as LifecycleDisabledReasonKey)
    : "unavailable";
}
