import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import {
  ACTIVITY_LABEL_EN,
  ACTIVITY_TONE_DOT_CLASS,
  activityPresentation,
  type ActivityEvent,
} from "./components/tabs/activity-event";
import {
  formatPresenceStatus,
  presenceStatusDotClass,
  presenceStatusVisual,
  toLiveAvailability,
} from "./presence";

// Activity projection tones — kind colour on the DOT only; label text stays
// neutral (mirrors the Activity timeline row).
const TONE_DOT_CLASS = ACTIVITY_TONE_DOT_CLASS;
const ACTIVITY_LABEL_TEXT = "text-foreground";

/**
 * Live Online/Offline view for profile / DM / side-panel headers (LRM-248).
 * Never carries Unstable / Reconnecting / Working / Idle / activity verbs.
 */
export type AgentLiveStatusView = {
  label: string;
  textClass: string;
  dotClass: string;
};

/** Active-task priority: running first, then starting, hold, queued. */
const ACTIVE_STATUS_RANK: Record<string, number> = {
  running: 0,
  dispatched: 1,
  waiting_local_directory: 2,
  queued: 3,
};

/**
 * Pick the single most relevant active task for an agent from the workspace
 * snapshot. Terminal rows are ignored (snapshot includes one terminal per
 * agent for last-activity, not current workload).
 */
export function pickPrimaryActiveTask(
  snapshot: readonly AgentTask[],
  agentId: string | null | undefined,
): AgentTask | null {
  if (!agentId) return null;
  let best: AgentTask | null = null;
  let bestRank = Number.POSITIVE_INFINITY;
  for (const task of snapshot) {
    if (task.agent_id !== agentId) continue;
    const rank = ACTIVE_STATUS_RANK[task.status];
    if (rank === undefined) continue;
    if (rank < bestRank) {
      best = task;
      bestRank = rank;
      continue;
    }
    if (rank === bestRank && best) {
      const taskTime = task.started_at ?? task.dispatched_at ?? task.created_at;
      const bestTime = best.started_at ?? best.dispatched_at ?? best.created_at;
      if (taskTime > bestTime) best = task;
    }
  }
  return best;
}

/**
 * Live presence for headers / profile cards — Online or Offline only.
 * `unstable` folds to Online. Archived returns null (caller shows gray avatar
 * + muted Archived secondary line; not a third live state).
 */
export function resolveAgentLiveStatus(args: {
  presence: AgentPresenceDetail | "loading" | null | undefined;
  /** @deprecated Ignored — live status no longer projects activity (LRM-248). */
  activeTask?: AgentTask | null;
  /** @deprecated Ignored — live status no longer projects activity (LRM-248). */
  latestActivity?: ActivityEvent | null;
  tAgents: TFunction<"agents">;
  /** @deprecated Unused for live Online/Offline. */
  tChat?: TFunction<"chat">;
}): AgentLiveStatusView | null {
  const { presence, tAgents } = args;
  if (!presence || presence === "loading") return null;
  if (toLiveAvailability(presence.availability) === null) return null;

  const label = formatPresenceStatus(presence, tAgents);
  const visual = presenceStatusVisual(presence);
  const dotClass = presenceStatusDotClass(presence);
  if (!label || !visual || !dotClass) return null;
  return { label, textClass: visual.textClass, dotClass };
}

/**
 * Activity-timeline projection for the composer strip (non-live event verbs:
 * Thinking / Running command…). Not a live presence label — Idle/Working/
 * Queued/Unstable/Reconnecting never appear here as presence words.
 *
 * Connection down (offline) suppresses activity verbs so a stale task cannot
 * paint "Running command" on an unreachable agent.
 */
export function resolveAgentActivityProjection(args: {
  presence: AgentPresenceDetail | "loading" | null | undefined;
  activeTask: AgentTask | null;
  latestActivity: ActivityEvent | null;
}): AgentLiveStatusView | null {
  const { presence, activeTask, latestActivity } = args;
  if (!presence || presence === "loading") return null;
  if (!activeTask) return null;

  const live = toLiveAvailability(presence.availability);
  // Archived / unknown — no activity strip.
  if (live === null) return null;
  // Offline — hide activity verbs (connection wins).
  if (live === "offline") return null;

  if (latestActivity) {
    const p = activityPresentation(latestActivity);
    const rawLabel = ACTIVITY_LABEL_EN[p.labelKey];
    const label =
      latestActivity.activity_kind === "tool_call" && p.tone === "active"
        ? `${rawLabel}…`
        : rawLabel;
    return {
      label,
      textClass: ACTIVITY_LABEL_TEXT,
      dotClass: TONE_DOT_CLASS[p.tone],
    };
  }

  // Task on the plate but nothing streamed yet → Thinking (timeline opener).
  return {
    label: ACTIVITY_LABEL_EN.thinking,
    textClass: ACTIVITY_LABEL_TEXT,
    dotClass: TONE_DOT_CLASS.neutral,
  };
}

/** Coarse Activity state for list/table contexts (task #7, 2026-07-31). */
export type AgentActivityBand = "idle" | "working" | "disconnected";

/**
 * Coarse Activity summary for surfaces that show every agent in a workspace
 * at once (agents list, delete-computer confirmation) — the SAME three-word
 * vocabulary as the rest of the Activity system (`ACTIVITY_LABEL_EN`), but
 * derived from the already-batched `AgentPresenceDetail` map
 * (`useWorkspacePresenceMap`) instead of a per-agent activity-event
 * subscription. Deliberately coarser than `resolveAgentActivityProjection`'s
 * Thinking/tool-call detail: showing that per row would mean N concurrent
 * event subscriptions in a list that can run to dozens of agents — the same
 * shape as a real click-lag regression under investigation elsewhere the
 * same day this was written. "Queued" folds into "working" here; the detail
 * page (which already pays for the richer subscription) is where a user
 * goes for the finer distinction.
 */
export function resolveAgentActivityBand(
  presence: AgentPresenceDetail | null | undefined,
): AgentActivityBand | null {
  if (!presence) return null;
  const live = toLiveAvailability(presence.availability);
  if (live === null || live === "offline") return "disconnected";
  return presence.workload === "idle" ? "idle" : "working";
}

export type AgentActivityBandView = {
  label: string;
  dotClass: string;
};

/**
 * Presentation for `resolveAgentActivityBand`. `showDisconnected` controls
 * whether a "disconnected" band renders as the word "Disconnected" or as a
 * bare em-dash — callers with an ADJACENT connectivity indicator (e.g. the
 * agents list's own Status column) must pass `false`, so the two cells don't
 * restate the same fact in different words (Frank, 2026-07-31: too much
 * duplicate information; Parker: "same fact once, one cell owns it" —
 * Activity answers "what is it doing", connectivity is Status's job alone).
 * Callers with no adjacent connectivity indicator (e.g. the delete-computer
 * confirmation's single-column table) pass `true`.
 */
export function presentAgentActivityBand(
  band: AgentActivityBand,
  showDisconnected: boolean,
): AgentActivityBandView {
  if (band === "disconnected" && !showDisconnected) {
    return { label: "—", dotClass: TONE_DOT_CLASS.neutral };
  }
  return {
    label: ACTIVITY_LABEL_EN[band],
    dotClass: TONE_DOT_CLASS[band === "working" ? "running" : "neutral"],
  };
}
