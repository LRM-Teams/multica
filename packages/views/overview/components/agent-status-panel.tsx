"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bot } from "lucide-react";
import { agentTaskSnapshotOptions } from "@multica/core/agents/queries";
import { agentListOptions } from "@multica/core/workspace/queries";
import type { AgentTask } from "@multica/core/types";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { useT, useTimeAgo } from "../../i18n";

const MAX_ITEMS = 8;

type StatusKind = "running" | "waiting" | "done" | "failed" | "cancelled";

// Active (non-terminal) statuses sort to the top so in-flight work is visible.
const ACTIVE = new Set<AgentTask["status"]>([
  "queued",
  "dispatched",
  "waiting_local_directory",
  "running",
]);

function kindOf(status: AgentTask["status"]): StatusKind {
  switch (status) {
    case "running":
      return "running";
    case "queued":
    case "dispatched":
    case "waiting_local_directory":
      return "waiting";
    case "completed":
      return "done";
    case "failed":
      return "failed";
    default:
      return "cancelled";
  }
}

const KIND_STYLE: Record<StatusKind, { dot: string; badge: string }> = {
  running: { dot: "bg-primary", badge: "border-primary/40 text-primary" },
  waiting: { dot: "bg-warning", badge: "border-warning/40 text-warning" },
  done: { dot: "bg-success", badge: "border-success/40 text-success" },
  failed: { dot: "bg-destructive", badge: "border-destructive/40 text-destructive" },
  cancelled: { dot: "bg-muted-foreground/40", badge: "text-muted-foreground" },
};

function taskTs(t: AgentTask): string {
  return t.completed_at || t.started_at || t.created_at;
}

/**
 * Real data: the workspace agent-task snapshot (every active task plus each
 * agent's most recent terminal task). Surfaces waiting / running / done /
 * failed states — not just terminal inbox notifications. Active tasks sort
 * first, then terminal tasks by recency.
 */
export function AgentStatusPanel({ wsId }: { wsId: string }) {
  const { t } = useT("overview");
  const timeAgo = useTimeAgo();

  const { data: tasks = [], isPending } = useQuery({
    ...agentTaskSnapshotOptions(wsId),
    enabled: !!wsId,
  });
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: !!wsId,
  });

  const nameById = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of agents) m.set(a.id, a.name);
    return m;
  }, [agents]);

  const rows = useMemo(() => {
    return [...tasks]
      .sort((a, b) => {
        const aActive = ACTIVE.has(a.status) ? 0 : 1;
        const bActive = ACTIVE.has(b.status) ? 0 : 1;
        if (aActive !== bActive) return aActive - bActive;
        return new Date(taskTs(b)).getTime() - new Date(taskTs(a)).getTime();
      })
      .slice(0, MAX_ITEMS);
  }, [tasks]);

  return (
    <Card size="sm" className="min-h-0">
      <CardHeader>
        <CardTitle>{t(($) => $.agent_status.title)}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {isPending ? (
          Array.from({ length: 4 }, (_, i) => <Skeleton key={i} className="h-10 w-full" />)
        ) : rows.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">
            {t(($) => $.agent_status.empty)}
          </p>
        ) : (
          rows.map((task) => {
            const kind = kindOf(task.status);
            const style = KIND_STYLE[kind];
            const name = nameById.get(task.agent_id) ?? "Agent";
            return (
              <div key={task.id} className="flex gap-3">
                <div className="flex size-7 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
                  <Bot className="size-3.5" />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-sm">{name}</span>
                    <Badge variant="outline" className={cn("shrink-0", style.badge)}>
                      {t(($) => $.agent_status.status[kind])}
                    </Badge>
                  </div>
                  <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                    <span className={cn("size-1.5 shrink-0 rounded-full", style.dot)} />
                    {task.trigger_summary ? (
                      <span className="min-w-0 truncate">{task.trigger_summary}</span>
                    ) : null}
                    <span className="shrink-0">{timeAgo(taskTs(task))}</span>
                  </span>
                </div>
              </div>
            );
          })
        )}
      </CardContent>
    </Card>
  );
}
