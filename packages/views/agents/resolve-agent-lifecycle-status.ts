import type { TFunction } from "i18next";
import type { AgentRuntimeDisplayStatus } from "@multica/core/types";

export type AgentLifecycleShape = "dot" | "square";

export interface AgentLifecycleStatusVisual {
  label: string;
  shape: AgentLifecycleShape;
  // All non-workload lifecycle states read grey — none of them is an alarm
  // state (Iris, 08-02: a deliberately-stopped machine isn't a fault; a
  // disconnected one may recover on its own).
  toneClass: string;
  dotClass: string;
}

const NEUTRAL_TONE = "text-muted-foreground";
const NEUTRAL_DOT = "bg-muted-foreground/40";
// Iris, 08-02: shape says "can it recover on its own" (dot = yes, square =
// no — stopped is the only terminal state); tone separately says "is this
// actively happening right now" — starting is a normal in-progress process
// (Starting → Idle is half the healthy sequence per the Raft manual), not
// a wait-for-recovery state, so it reads brand like update-in-progress
// (RUNTIME_HEALTH_STATE_VISUAL's updating/ready_to_apply), not grey.
const BRAND_TONE = "text-brand";
const BRAND_DOT = "bg-brand";

/**
 * Presentation for the machine-detail / agent-side-panel health area — NOT
 * the always-visible presence dot (`../agents/presence.ts`), which stays
 * the LRM-248-locked binary Online/Offline (Parker, 08-02: not reopened).
 *
 * Reads `runtime_display_status` (server/internal/handler/agent_health.go's
 * `agentRuntimeDisplayStatus`). Returns null for "idle"/"working" — those
 * already have their own presentation via activity-event.ts, this function
 * doesn't duplicate it.
 *
 * A missing/unrecognized value (older backend, or a status this FE doesn't
 * know about yet) falls back to the generic "offline" visual — never guess
 * a more specific state (stopped vs disconnected vs crashed) from an absent
 * field. "crashed" isn't emitted by the backend yet (task #1803) but is
 * handled here already so turning it on server-side is a zero-FE-change flip.
 */
export function resolveAgentLifecycleStatus(
  status: AgentRuntimeDisplayStatus | null | undefined,
  t: TFunction<"agents">,
): AgentLifecycleStatusVisual | null {
  switch (status) {
    case "idle":
    case "working":
      return null;
    case "stopped":
      return {
        label: t(($) => $.lifecycle_status.stopped),
        shape: "square",
        toneClass: NEUTRAL_TONE,
        dotClass: NEUTRAL_DOT,
      };
    case "starting":
      return {
        label: t(($) => $.lifecycle_status.starting),
        shape: "dot",
        toneClass: BRAND_TONE,
        dotClass: BRAND_DOT,
      };
    case "crashed":
      return {
        label: t(($) => $.lifecycle_status.crashed),
        shape: "dot",
        toneClass: NEUTRAL_TONE,
        dotClass: NEUTRAL_DOT,
      };
    case "blocked":
      return {
        label: t(($) => $.lifecycle_status.blocked),
        shape: "dot",
        toneClass: NEUTRAL_TONE,
        dotClass: NEUTRAL_DOT,
      };
    case "disconnected":
      return {
        label: t(($) => $.lifecycle_status.disconnected),
        shape: "dot",
        toneClass: NEUTRAL_TONE,
        dotClass: NEUTRAL_DOT,
      };
    case "offline":
    default:
      return {
        label: t(($) => $.lifecycle_status.offline),
        shape: "dot",
        toneClass: NEUTRAL_TONE,
        dotClass: NEUTRAL_DOT,
      };
  }
}
