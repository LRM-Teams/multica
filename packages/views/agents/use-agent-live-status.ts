"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  agentTaskSnapshotOptions,
  useAgentPresenceDetail,
} from "@multica/core/agents";
import type { TaskMessagePayload } from "@multica/core/types";
import { useT } from "../i18n/use-t";
import {
  pickPrimaryActiveTask,
  resolveAgentLiveStatus,
  type AgentLiveStatusView,
} from "./resolve-agent-live-status";
import { useAgentActivityEvents } from "./components/tabs/use-agent-activity-events";
import type { ActivityEvent } from "./components/tabs/activity-event";

/**
 * Project the agent's Activity event stream (#302 one-read-model) into the
 * TaskMessagePayload shape the shared stage picker (`pickStageKeys`) reads. Only
 * the working kinds carry a stage word: thinking / text / tool_call — the latest
 * of these decides the name-row label; everything else (status rows, errors,
 * output-results) is ignored by the picker. `pickStageKeys` only reads `.type`
 * and `.tool`, so those are the only fields we fill.
 *
 * This replaces the old per-round fetch of `/api/tasks/{id}/messages`, which
 * projected these SAME Activity events server-side behind an inbox-event-id
 * compat (#525). The projection now lives on the FE, so the old task endpoint no
 * longer has to masquerade inbox ids as task ids (#414 — Frank: the temp bridge
 * doesn't become a long-term contract).
 */
export function activityEventsToTaskMessages(
  events: readonly ActivityEvent[],
): TaskMessagePayload[] {
  const out: TaskMessagePayload[] = [];
  for (const e of events) {
    const type =
      e.activity_kind === "thinking"
        ? "thinking"
        : e.activity_kind === "text"
          ? "text"
          : e.activity_kind === "tool_call"
            ? "tool_use"
            : null;
    if (!type) continue;
    out.push({
      task_id: "",
      issue_id: "",
      seq: out.length,
      type,
      tool: type === "tool_use" ? e.tool : undefined,
      created_at: e.occurred_at,
    });
  }
  return out;
}

/**
 * Live name-row status for an agent: stage-detail when a task is active
 * (Thinking / Running command… / …), presence word when idle. The active-task
 * gate still comes from the workspace snapshot; the stage-detail now comes from
 * the Activity event stream (#414), scoped to the CURRENT round so a freshly
 * queued task — nothing streamed yet — still reads "Queued", not a stale
 * "running" from history. WS `agent_activity:event` keeps it live in lockstep
 * with the Activity tab (same shared cache).
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
  // This mirrors the old query's `enabled: !!activeTask?.id`.
  const { events } = useAgentActivityEvents(activeTask ? (agentId ?? "") : "");
  const roundStart = activeTask?.started_at ?? activeTask?.dispatched_at ?? null;
  const taskMessages = useMemo(
    () =>
      roundStart
        ? activityEventsToTaskMessages(
            events.filter((e) => e.occurred_at >= roundStart),
          )
        : [],
    [events, roundStart],
  );

  return useMemo(
    () =>
      resolveAgentLiveStatus({
        presence,
        activeTask,
        taskMessages,
        tAgents,
        tChat,
      }),
    [presence, activeTask, taskMessages, tAgents, tChat],
  );
}
