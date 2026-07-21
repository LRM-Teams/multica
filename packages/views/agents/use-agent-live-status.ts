"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  agentTaskSnapshotOptions,
  useAgentPresenceDetail,
} from "@multica/core/agents";
import { useT } from "../i18n/use-t";
import {
  pickPrimaryActiveTask,
  resolveAgentLiveStatus,
  type AgentLiveStatusView,
} from "./resolve-agent-live-status";
import { useAgentActivityEvents } from "./components/tabs/use-agent-activity-events";

// The header projects the latest WORK row of the current round — thinking /
// tool_call — so it reads the actual action (Running command · / Thinking),
// never a generic "Working" status row. Status rows (agent_status_changed) and
// diagnostics are skipped here; the active-vs-idle gate is the snapshot's
// active task, not these events.
//
// LRM-202: deliberately exclude `text` — Activity presents those as "Output",
// which Frank does not want mirrored above the composer (or on the live name
// row). Streaming prose falls back to the prior thinking/tool verb, or
// Thinking when nothing else has streamed yet.
const HEADER_STAGE_KINDS = new Set(["thinking", "tool_call"]);

/**
 * Live name-row status for an agent. While a task is active the header projects
 * the SAME latest Activity row the timeline shows — word AND type-based dot
 * colour (Parker: header = Activity latest-row projection, no separate stage
 * vocabulary) — and the coarse presence word (Idle / Offline / …) when nothing is
 * on the plate. The active-task gate comes from the workspace snapshot; the
 * projected row comes from the Activity event stream (#302 one-read-model, #414 —
 * off the old task-message bridge), scoped to the CURRENT round so a freshly
 * queued task (nothing streamed yet) still reads "Queued", not a stale row. WS
 * `agent_activity:event` keeps it live in lockstep with the Activity tab.
 */
export function useAgentLiveStatus(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentLiveStatusView | null {
  const { t: tAgents } = useT("agents");
  const { t: tChat } = useT("chat");
  const presence = useAgentPresenceDetail(wsId, agentId);
  const { data: snapshot = [] } = useQuery({
    ...agentTaskSnapshotOptions(wsId ?? ""),
    enabled: !!wsId && !!agentId,
  });
  const activeTask = useMemo(
    () => pickPrimaryActiveTask(snapshot, agentId),
    [snapshot, agentId],
  );
  // Only pull the Activity stream when a task is actually active — passing "" to
  // the hook (which gates on `enabled: !!agentId`) keeps idle agents in a list
  // from each fetching a full event stream for what is just a presence word.
  const { events } = useAgentActivityEvents(activeTask ? (agentId ?? "") : "");
  const roundStart = activeTask?.started_at ?? activeTask?.dispatched_at ?? null;
  const latestActivity = useMemo(() => {
    if (!roundStart) return null;
    let latest = null;
    for (const e of events) {
      if (e.occurred_at >= roundStart && HEADER_STAGE_KINDS.has(e.activity_kind)) {
        latest = e; // events are chronologically ordered; last match wins
      }
    }
    return latest;
  }, [events, roundStart]);

  return useMemo(
    () =>
      resolveAgentLiveStatus({
        presence,
        activeTask,
        latestActivity,
        tAgents,
        tChat,
      }),
    [presence, activeTask, latestActivity, tAgents, tChat],
  );
}
