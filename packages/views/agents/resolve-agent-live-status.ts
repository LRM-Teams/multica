import type { TFunction } from "i18next";
import type { AgentPresenceDetail } from "@multica/core/agents";
import {
  formatPresenceStatus,
  presenceStatusDotClass,
  presenceStatusVisual,
  toLiveAvailability,
} from "./presence";

/**
 * Live Online/Offline view for profile / DM / side-panel headers (LRM-248).
 * Never carries Unstable / Reconnecting / Working / Idle / activity verbs.
 */
export type AgentLiveStatusView = {
  label: string;
  textClass: string;
  dotClass: string;
};

/**
 * Live presence for headers / profile cards — Online or Offline only.
 * `unstable` folds to Online. Archived returns null (caller shows gray avatar
 * + muted Archived secondary line; not a third live state).
 */
export function resolveAgentLiveStatus(args: {
  presence: AgentPresenceDetail | "loading" | null | undefined;
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
    return { label: "—", dotClass: "bg-muted-foreground" };
  }
  return {
    label: band === "idle" ? "Idle" : band === "working" ? "Working" : "Disconnected",
    dotClass: band === "working" ? "bg-brand" : "bg-muted-foreground",
  };
}
