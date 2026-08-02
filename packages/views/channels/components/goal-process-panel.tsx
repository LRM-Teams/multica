"use client";

import { useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { BookMarked, RefreshCw, X } from "lucide-react";
import type { ChannelGoal, ChannelGoalProcessMarkdown, ChannelMember } from "@multica/core/types";
import {
  channelGoalProcessOptions,
  channelGoalProcessesOptions,
  channelMemberRole,
  channelMembersOptions,
} from "@multica/core/channels";
import { useWorkspacePaths } from "@multica/core/paths";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { Markdown } from "../../common/markdown";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import { useTimeAgo } from "../../i18n/use-time-ago";

export const GOAL_PROCESS_PANEL_ID = "channel-goal-process-panel";

const EMPTY_MEMBERS: ChannelMember[] = [];
const EMPTY_PROCESSES: ChannelGoalProcessMarkdown[] = [];

function managerAgents(members: ChannelMember[]): ChannelMember[] {
  return members.filter(
    (member) => member.member_type === "agent" && channelMemberRole(member) === "manager",
  );
}

function newestManagerId(
  processes: ChannelGoalProcessMarkdown[],
  managers: ChannelMember[],
): string {
  let bestId = "";
  let bestTs = Number.NEGATIVE_INFINITY;
  for (const doc of processes) {
    if (!managers.some((m) => m.member_id === doc.manager_agent_id)) continue;
    const ts = Date.parse(doc.updated_at);
    if (Number.isFinite(ts) && ts > bestTs) {
      bestTs = ts;
      bestId = doc.manager_agent_id;
    }
  }
  return bestId;
}

function resolveDefaultManagerId({
  managerId,
  currentUserId,
  managers,
  processes,
}: {
  managerId?: string;
  currentUserId?: string;
  managers: ChannelMember[];
  processes: ChannelGoalProcessMarkdown[];
}): string {
  if (managerId && managers.some((m) => m.member_id === managerId)) return managerId;
  if (currentUserId && managers.some((m) => m.member_id === currentUserId)) {
    return currentUserId;
  }
  const newest = newestManagerId(processes, managers);
  if (newest) return newest;
  return managers[0]?.member_id ?? "";
}

/** LRM-932 (jianghp3 seq5569): process viewer under the top Goal card — not a forced right rail. */
export function GoalProcessPanel({
  channelId,
  goal,
  managerId,
  onManagerChange,
  onClose,
  currentUserId,
}: {
  channelId: string;
  goal: ChannelGoal;
  managerId?: string;
  onManagerChange?: (managerId: string) => void;
  onClose: () => void;
  currentUserId?: string;
}) {
  const { t } = useT("channels");
  const { t: tKnowledge } = useT("knowledge");
  const paths = useWorkspacePaths();
  const timeAgo = useTimeAgo();
  const { data: membersData } = useQuery(channelMembersOptions(channelId));
  const members = membersData ?? EMPTY_MEMBERS;
  const managers = useMemo(() => managerAgents(members), [members]);
  const { data: processesData, refetch: refetchProcesses } = useQuery(
    channelGoalProcessesOptions(channelId),
  );
  const processes = processesData?.processes ?? EMPTY_PROCESSES;

  const defaultManagerId = useMemo(
    () =>
      resolveDefaultManagerId({
        managerId,
        currentUserId,
        managers,
        processes,
      }),
    [managerId, managers, currentUserId, processes],
  );

  // User tab pick only; fall back to derived default (no effect sync).
  const [pickedManagerId, setPickedManagerId] = useState<string | null>(null);
  const activeManagerId =
    pickedManagerId && managers.some((m) => m.member_id === pickedManagerId)
      ? pickedManagerId
      : defaultManagerId;

  const selectManager = (next: string) => {
    setPickedManagerId(next);
    onManagerChange?.(next);
  };

  const {
    data: processData,
    isPending: processPending,
    isError: processError,
    isFetching: processFetching,
    refetch: refetchProcess,
  } = useQuery(channelGoalProcessOptions(channelId, activeManagerId));
  const processDoc = processData?.process ?? null;
  const activeManager = managers.find((m) => m.member_id === activeManagerId);

  const refresh = () => {
    void refetchProcesses();
    void refetchProcess();
  };

  const shortStatus = (
    <div className="space-y-2 rounded-md border border-border/50 bg-muted/20 px-3 py-2.5">
      <div>
        <div className="text-[9.5px] font-bold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.goal.current_step)}
        </div>
        <p className="mt-0.5 text-sm font-medium leading-snug">
          {goal.current_step?.trim() ? (
            goal.current_step
          ) : (
            <span className="italic text-muted-foreground">{t(($) => $.goal.none)}</span>
          )}
        </p>
      </div>
      <div>
        <div className="text-[9.5px] font-bold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.goal.blocker_label)}
        </div>
        <p
          className={cn(
            "mt-0.5 text-sm leading-snug",
            goal.blocker?.trim() ? "font-medium text-destructive" : "italic text-muted-foreground",
          )}
        >
          {goal.blocker?.trim() ? goal.blocker : t(($) => $.goal.none)}
        </p>
      </div>
      <div>
        <div className="text-[9.5px] font-bold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.goal.progress)}
        </div>
        <p className="mt-0.5 text-sm leading-snug text-muted-foreground">
          {goal.progress_summary?.trim() ? (
            goal.progress_summary
          ) : (
            <span className="italic">{t(($) => $.goal.none)}</span>
          )}
        </p>
      </div>
    </div>
  );

  let body: ReactNode;
  if (!activeManagerId) {
    body = (
      <div className="px-1 py-6 text-center text-sm text-muted-foreground">
        {t(($) => $.goal.process_empty)}
      </div>
    );
  } else if (processPending && !processDoc) {
    body = (
      <div className="space-y-3">
        {shortStatus}
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-5/6" />
        <Skeleton className="h-20 w-full" />
      </div>
    );
  } else if (processError) {
    body = (
      <div className="flex flex-col items-center gap-3 py-8 text-center">
        <p className="text-sm text-destructive">{t(($) => $.goal.process_error)}</p>
        <Button size="sm" variant="outline" onClick={() => void refetchProcess()}>
          {t(($) => $.goal.retry)}
        </Button>
      </div>
    );
  } else if (!processDoc || !processDoc.content.trim()) {
    body = (
      <div className="space-y-3">
        {shortStatus}
        <div className="rounded-md border border-dashed border-border/60 px-4 py-8 text-center text-sm text-muted-foreground">
          {t(($) => $.goal.process_empty_for, {
            name: activeManager?.display_name || activeManager?.name || "—",
          })}
        </div>
      </div>
    );
  } else {
    body = (
      <div className="space-y-3">
        {shortStatus}
        <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
          <span>
            {t(($) => $.goal.updated_at, {
              when: timeAgo(processDoc.updated_at),
            })}
          </span>
          <span>
            {t(($) => $.goal.process_version, { version: processDoc.version })}
          </span>
        </div>
        {processFetching ? (
          <p className="text-[11px] text-muted-foreground">{t(($) => $.goal.process_updating)}</p>
        ) : null}
        <div className="prose prose-sm dark:prose-invert max-w-none text-sm">
          <Markdown mode="full">{processDoc.content}</Markdown>
        </div>
      </div>
    );
  }

  return (
    <section
      id={GOAL_PROCESS_PANEL_ID}
      aria-label={t(($) => $.goal.process)}
      data-testid="goal-process-panel"
      className="border-t border-border/40 bg-background"
    >
      <div className="flex items-center justify-between gap-2 px-4 py-2">
        <div className="min-w-0">
          <div className="text-xs font-semibold">{t(($) => $.goal.process)}</div>
          <p className="truncate text-[11px] text-muted-foreground">{goal.title}</p>
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={tKnowledge(($) => $.drill.open_wiki)}
            render={<AppLink href={`${paths.wiki()}?tab=goal`} />}
          >
            <BookMarked className="size-3.5" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={t(($) => $.goal.process_refresh)}
            onClick={refresh}
          >
            <RefreshCw className={cn("size-3.5", processFetching && "animate-spin")} />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-7"
            aria-label={t(($) => $.goal.process_close)}
            onClick={onClose}
          >
            <X className="size-3.5" />
          </Button>
        </div>
      </div>
      {managers.length >= 2 ? (
        <Tabs
          value={activeManagerId}
          onValueChange={selectManager}
          className="shrink-0 border-b border-border/40 px-2"
        >
          <TabsList
            variant="line"
            className="h-auto w-full justify-start overflow-x-auto rounded-none bg-transparent p-0"
            role="tablist"
          >
            {managers.map((manager) => (
              <TabsTrigger
                key={manager.member_id}
                value={manager.member_id}
                className="gap-1.5 px-2.5 py-2 text-xs data-active:shadow-none"
              >
                <ActorAvatar
                  actorType="agent"
                  actorId={manager.member_id}
                  size={18}
                  name={manager.display_name || manager.name}
                  avatarUrlHint={manager.avatar_url}
                  showStatusDot={false}
                  profileLink={false}
                />
                <span className="max-w-[7rem] truncate">
                  {manager.display_name || manager.name}
                </span>
              </TabsTrigger>
            ))}
          </TabsList>
          {managers.map((manager) => (
            <TabsContent key={manager.member_id} value={manager.member_id} className="m-0" />
          ))}
        </Tabs>
      ) : null}
      <div className="max-h-[min(50vh,28rem)] overflow-y-auto px-4 py-3">{body}</div>
    </section>
  );
}
