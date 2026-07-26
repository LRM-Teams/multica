import type { AgentDMControlAction } from "@multica/core/dm";
import type { InboxItem } from "@multica/core/types";
import {
  AGENT_DM_PAUSE_EVENTS,
  type AgentDMPauseSystemEvent,
} from "../../channels/components/channel-system-event";

const AGENT_DM_PAUSE_EVENT_KINDS = new Set<string>(Object.values(AGENT_DM_PAUSE_EVENTS));

/**
 * The owner-private `agent_dm_paused` inbox item (#692, FE-6) — the same A2A
 * gate payload the DM-internal system row carries (FE-5), delivered to the
 * agent owner's Activity/Inbox as a proactive alert. The backend stamps the
 * event_params onto `InboxItem.details`; that field is typed as a flat string
 * map for the other kinds, but this kind's payload is a richer object (numbers
 * + an `actions` array), so we read it through an `unknown` cast and validate.
 *
 * Pure (no JSX) so the component file can stay Fast-Refresh clean
 * (`only-export-components`).
 */
export interface AgentDMPausedInbox {
  /** Reuses FE-5's parsed shape so the row can render the same localized copy. */
  system: AgentDMPauseSystemEvent;
  dmChannelId: string;
  exchangeId?: string;
  /** Owner actions the backend offers on this alert. */
  actions: AgentDMControlAction[];
}

function detailString(d: Record<string, unknown>, key: string): string | undefined {
  const value = d[key];
  return typeof value === "string" && value ? value : undefined;
}

// Numeric params arrive as JSON numbers even though `details` is typed as a
// string map — accept either so a number that round-trips as a string still reads.
function detailNumber(d: Record<string, unknown>, key: string): number | undefined {
  const value = d[key];
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const n = Number(value);
    if (Number.isFinite(n)) return n;
  }
  return undefined;
}

export function parseAgentDMPausedInbox(item: InboxItem): AgentDMPausedInbox | null {
  if (item.type !== "agent_dm_paused" || !item.details) return null;
  const d = item.details as unknown as Record<string, unknown>;
  const event = detailString(d, "system_event");
  const dmChannelId = detailString(d, "dm_channel_id");
  if (!event || !AGENT_DM_PAUSE_EVENT_KINDS.has(event) || !dmChannelId) return null;
  const rawActions = d.actions;
  const actions = Array.isArray(rawActions)
    ? rawActions.filter((a): a is AgentDMControlAction => typeof a === "string")
    : [];
  return {
    system: {
      event: event as AgentDMPauseSystemEvent["event"],
      matter: detailString(d, "matter") ?? "",
      round: detailNumber(d, "round") ?? 0,
      roundLimit: detailNumber(d, "round_limit") ?? 0,
      agentAName: detailString(d, "agent_a_name") ?? detailString(d, "agent_a_handle") ?? "",
      agentBName: detailString(d, "agent_b_name") ?? detailString(d, "agent_b_handle") ?? "",
    },
    dmChannelId,
    exchangeId: detailString(d, "exchange_id"),
    actions,
  };
}
