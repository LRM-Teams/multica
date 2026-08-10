import type { TFunction } from "i18next";
import type { AgentPresence } from "@multica/core/agents";
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
 * Entity lifecycle is handled by callers.
 */
export function resolveAgentLiveStatus(args: {
  presence: AgentPresence | "loading" | null | undefined;
  tAgents: TFunction<"agents">;
  /** @deprecated Unused for live Online/Offline. */
  tChat?: TFunction<"chat">;
}): AgentLiveStatusView | null {
  const { presence, tAgents } = args;
  if (!presence || presence === "loading") return null;
  if (toLiveAvailability(presence) === null) return null;

  const label = formatPresenceStatus(presence, tAgents);
  const visual = presenceStatusVisual(presence);
  const dotClass = presenceStatusDotClass(presence);
  if (!label || !visual || !dotClass) return null;
  return { label, textClass: visual.textClass, dotClass };
}
