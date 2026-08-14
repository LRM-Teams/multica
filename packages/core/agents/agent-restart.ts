import type {
  AgentRestartMode,
  AgentRestartModeState,
  AgentRestartPreflight,
} from "../types";

/**
 * Pure helpers for the Raft-aligned Agent reset modes. Kept UI-free so the
 * terminal-set (poll-stop) discipline and the per-mode preflight accessor are
 * unit-testable without React — mirrors `runtimes/update-status.ts` (#687).
 */

/**
 * The server-authoritative executability for one tier. Returns a hard-disabled
 * fallback when the preflight is missing or omits the action, so the UI fails
 * closed (disabled) rather than offering an action the server hasn't blessed —
 * this also covers the dormant `unsupported_runtime_capability` window before
 * #677 D6 advertises the capability.
 */
export function agentRestartModeState(
  preflight: AgentRestartPreflight | undefined | null,
  kind: AgentRestartMode,
): AgentRestartModeState {
  const state = preflight?.actions?.[kind];
  if (!state) {
    return { supported: false, disabled_reason: "unavailable" };
  }
  return state;
}

/**
 * `disabled_reason` values the FE has copy for (`agents.json`'s
 * `restart_modal.disabled_reason`). Shared between the restart modal's
 * per-tier list and the trigger button's standing reason line so both read
 * the same server field through the same key set — never re-derive this
 * from `agent.status`.
 */
export const KNOWN_RESTART_DISABLED_REASONS = new Set([
  "agent_active",
  // The `unsupported_runtime_capability` copy (agents.json's
  // restart_modal.disabled_reason) names a specific daemon version
  // (currently v0.3.95) — that's the daemon build that first advertises
  "unsupported_runtime_capability",
  "no_runtime",
  "offline",
  "no_permission",
]);

export type RestartDisabledReasonKey =
  | "agent_active"
  | "unsupported_runtime_capability"
  | "no_runtime"
  | "offline"
  | "no_permission"
  | "unavailable";

/** Maps a server `disabled_reason` to a known copy key, falling back to "unavailable". */
export function resolveRestartDisabledReasonKey(
  reason: string | null | undefined,
): RestartDisabledReasonKey {
  return reason && KNOWN_RESTART_DISABLED_REASONS.has(reason)
    ? (reason as RestartDisabledReasonKey)
    : "unavailable";
}
