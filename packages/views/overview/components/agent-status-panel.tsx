"use client";

import { useMemo } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Virtuoso } from "react-virtuoso";
import { Bot } from "lucide-react";
import { agentTaskFeedOptions } from "@multica/core/agents/queries";
import { agentListOptions } from "@multica/core/workspace/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import type { AgentTaskFeedItem } from "@multica/core/types";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import { agentColor } from "../../common/agent-color";
import { stripMentionMarkdown } from "../../common/strip-mention-markdown";
import { AppLink } from "../../navigation";
import { useT, useTimeAgo } from "../../i18n";

type StatusKind = "done" | "failed" | "cancelled";

function kindOf(status: string): StatusKind {
  if (status === "failed") return "failed";
  if (status === "cancelled") return "cancelled";
  return "done";
}

// Color of the inline verb ("completed" / "failed" / "cancelled") so the row's
// outcome reads at a glance without a separate badge.
const VERB_CLASS: Record<StatusKind, string> = {
  done: "text-success",
  failed: "text-destructive",
  cancelled: "text-muted-foreground",
};

// One completed-task row, phrased as a sentence: "<agent> <verb> <what>".
// The "what" is the task's own instruction (trigger_summary) when present,
// else the linked issue's title — so every row says what the agent actually
// did, not just who did it. Calling hooks here is fine: Virtuoso only mounts
// the handful of rows near the viewport, so the cost stays bounded even when
// the feed holds thousands of tasks.
function TaskRow({ task, name }: { task: AgentTaskFeedItem; name: string }) {
  const { t } = useT("overview");
  const timeAgo = useTimeAgo();
  const p = useWorkspacePaths();

  const kind = kindOf(task.status);
  const color = agentColor(task.agent_id);
  // What the agent actually did, best source first: its own instruction, then
  // the linked issue's title, then the chat session's topic. Generic fallback
  // so a row never reads as just "<agent> completed".
  //
  // These are author-sourced strings (an agent's own trigger instruction, an
  // issue/chat title) that still carry mention markdown like
  // `[@Nash](mention://agent/id)`. This row is a single-line truncated preview
  // inside a jump `AppLink`, so — like the agent Activity timeline (#387) — we
  // normalize mentions to their display name (`@Nash`) rather than leak the raw
  // `mention://` URI; real markdown links (`[docs](https://…)`) are untouched.
  const description = stripMentionMarkdown(
    task.trigger_summary ||
      task.issue_title ||
      task.chat_title ||
      t(($) => $.agent_status.fallback_task),
  );

  const inner = (
    <>
      <div
        className="flex size-7 shrink-0 items-center justify-center rounded-full"
        style={{ backgroundColor: color.bg, color: color.fg }}
      >
        <Bot className="size-3.5" />
      </div>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm">
          <span className="font-medium text-foreground">{name}</span>
          <span className={cn("ml-1", VERB_CLASS[kind])}>
            {t(($) => $.agent_status.status[kind])}
          </span>
          {description ? (
            <span className="ml-1 text-muted-foreground">{description}</span>
          ) : null}
        </p>
        <div className="mt-0.5 flex items-center gap-2 text-[11px] text-muted-foreground">
          {task.completed_at ? <span>{timeAgo(task.completed_at)}</span> : null}
          {task.issue_identifier ? (
            <span className="font-mono opacity-70">{task.issue_identifier}</span>
          ) : null}
        </div>
      </div>
    </>
  );

  const rowClass = "flex gap-3 border-b px-3 py-2.5";
  return task.issue_id ? (
    <AppLink
      href={p.issueDetail(task.issue_id)}
      className={cn(rowClass, "transition-colors hover:bg-accent")}
    >
      {inner}
    </AppLink>
  ) : (
    <div className={rowClass}>{inner}</div>
  );
}

/**
 * Real data: a workspace-wide feed of every terminal agent task
 * (completed/failed/cancelled), newest first — one row per task. Backed by a
 * cursor-paginated endpoint and rendered with a virtualized infinite-scroll
 * list, so a workspace with thousands of completions stays smooth and the user
 * just scrolls to load more.
 */
export function AgentStatusPanel({ wsId }: { wsId: string }) {
  const { t } = useT("overview");

  const { data, isPending, fetchNextPage, hasNextPage, isFetchingNextPage } =
    useInfiniteQuery(agentTaskFeedOptions(wsId));

  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: !!wsId,
  });
  const nameById = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of agents) m.set(a.id, a.name);
    return m;
  }, [agents]);

  const tasks = useMemo(() => data?.pages.flatMap((pg) => pg.tasks) ?? [], [data]);

  return (
    <Card size="sm" className="h-full min-h-0">
      <CardHeader>
        <CardTitle>{t(($) => $.agent_status.title)}</CardTitle>
      </CardHeader>
      <CardContent className="min-h-0 flex-1 p-0">
        {isPending ? (
          <div className="flex flex-col gap-3 px-3">
            {Array.from({ length: 5 }, (_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : tasks.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">
            {t(($) => $.agent_status.empty)}
          </p>
        ) : (
          <Virtuoso
            className="h-full"
            data={tasks}
            increaseViewportBy={{ top: 200, bottom: 400 }}
            endReached={() => {
              if (hasNextPage && !isFetchingNextPage) void fetchNextPage();
            }}
            itemContent={(_, task) => (
              <TaskRow task={task} name={nameById.get(task.agent_id) ?? "Agent"} />
            )}
            components={{
              Footer: () =>
                isFetchingNextPage ? (
                  <div className="px-3 py-2">
                    <Skeleton className="h-9 w-full" />
                  </div>
                ) : null,
            }}
          />
        )}
      </CardContent>
    </Card>
  );
}
