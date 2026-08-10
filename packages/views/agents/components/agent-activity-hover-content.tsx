"use client";

import { useEffect, useState } from "react";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { useActorName } from "@multica/core/workspace/hooks";
import type { AgentTask } from "@multica/core/types";
import { workloadConfig } from "../presence";
import { useT } from "../../i18n";

interface AgentActivityHoverContentProps {
  // Active tasks (running / queued / dispatched) to render — caller filters
  // by issue id or by workspace scope. Order is preserved; we render every
  // task as its own row.
  tasks: readonly AgentTask[];
}

/**
 * Shared hover-card body for "what are these agents doing right now?" — used
 * by IssueAgentActivityIndicator (per-issue) and WorkspaceAgentWorkingChip
 * (workspace-wide). One row per task: agent avatar, name, status dot,
 * status label, duration.
 *
 * This is Task workload, not Presence. Its label and tone are derived only
 * from the Task state and never from Runtime reachability or Agent Presence.
 */
export function AgentActivityHoverContent({
  tasks,
}: AgentActivityHoverContentProps) {
  const { t } = useT("issues");
  const { getActorName, getActorInitials, getActorAvatarUrl } = useActorName();

  // Tick `now` once per second so the per-task duration label updates
  // live while the hover card is open. setInterval only runs while the
  // hover card is mounted (Base UI portals the content but tears it down
  // on close), so this costs nothing when the card is closed.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  if (tasks.length === 0) return null;

  return (
    <div className="flex flex-col gap-2">
      <div className="text-xs font-medium text-muted-foreground">
        {t(($) => $.agent_activity.hover_header, { count: tasks.length })}
      </div>
      <div className="flex flex-col gap-1.5">
        {tasks.map((task) => {
          const isRunning = task.status === "running";
          // queued/dispatched both read as "queued" in the user-facing
          // copy — `dispatched` is the daemon-acked sub-state of queued
          // and not user-meaningful here.
          const wl = isRunning ? workloadConfig.working : workloadConfig.queued;
          const dotClass = isRunning ? "bg-brand" : "bg-muted-foreground/40";
          const labelClass = wl.textClass;
          const startedFrom = isRunning
            ? (task.started_at ?? task.dispatched_at ?? task.created_at)
            : task.created_at;

          return (
            <div
              key={task.id}
              className="flex items-center gap-2 text-xs"
            >
              <ActorAvatarBase
                name={getActorName("agent", task.agent_id)}
                initials={getActorInitials("agent", task.agent_id)}
                avatarUrl={getActorAvatarUrl("agent", task.agent_id)}
                isAgent
                size={18}
              />
              <span className="flex-1 truncate font-medium">
                {getActorName("agent", task.agent_id)}
              </span>
              <span className="flex shrink-0 items-center gap-1.5">
                <span className={`h-1.5 w-1.5 rounded-full ${dotClass}`} />
                <span className={labelClass}>
                  {isRunning
                    ? t(($) => $.agent_activity.status_running)
                    : t(($) => $.agent_activity.status_queued)}
                </span>
                <span className="tabular-nums text-muted-foreground">
                  {formatDuration(startedFrom, now)}
                </span>
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// Compact `2m 14s` / `45s` / `1h 03m` duration since the given ISO string.
// Capped at hours — anything over a day for a running task is a sign of a
// stuck runtime, but the hover card is not the place to relitigate that;
// the row will read as `26h 12m` and the user can act.
//
// Exported so the issue-detail header live chip formats its collapsed
// single-agent elapsed with the same `2m 14s` / `1h 03m` rule used here.
export function formatDuration(fromIso: string, nowMs: number): string {
  const start = new Date(fromIso).getTime();
  if (!Number.isFinite(start)) return "";
  const sec = Math.max(0, Math.round((nowMs - start) / 1000));
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  const remSec = sec % 60;
  if (min < 60) return `${min}m ${pad2(remSec)}s`;
  const hr = Math.floor(min / 60);
  const remMin = min % 60;
  return `${hr}h ${pad2(remMin)}m`;
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}
