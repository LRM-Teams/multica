"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import type { ChannelGoal, ChannelMember } from "@multica/core/types";
import {
  channelGoalProcessOptions,
  channelGoalProcessesOptions,
  channelMemberRole,
  channelMembersOptions,
} from "@multica/core/channels";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";
import { ConversationSidePanelShell } from "../../common/conversation-side-panel-shell";
import { ActorAvatar } from "../../common/actor-avatar";
import { Markdown } from "../../common/markdown";
import { useT } from "../../i18n";
import { useTimeAgo } from "../../i18n/use-time-ago";

const PANEL_ID = "channel-goal-process-panel";

function managerAgents(members: ChannelMember[]): ChannelMember[] {
  return members.filter(
    (member) => member.member_type === "agent" && channelMemberRole(member) === "manager",
  );
}

export function GoalProcessPanel({
  channelId,
  goal,
  managerId,
  onManagerChange,
  onClose,
  currentUserId,
  variant = "panel",
}: {
  channelId: string;
  goal: ChannelGoal;
  managerId?: string;
  onManagerChange: (managerId: string) => void;
  onClose: () => void;
  currentUserId?: string;
  variant?: "panel" | "page";
}) {
  const { t } = useT("channels");
  const timeAgo = useTimeAgo();
  const { data: members = [] } = useQuery(channelMembersOptions(channelId));
  const managers = useMemo(() => managerAgents(members), [members]);
  const processesQuery = useQuery(channelGoalProcessesOptions(channelId));
  const processes = processesQuery.data?.processes ?? [];

  const defaultManagerId = useMemo(() => {
    if (managerId && managers.some((m) => m.member_id === managerId)) return managerId;
    if (currentUserId && managers.some((m) => m.member_id === currentUserId)) {
      return currentUserId;
    }
    const newest = [...processes].sort(
      (a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at),
    )[0];
    if (newest && managers.some((m) => m.member_id === newest.manager_agent_id)) {
      return newest.manager_agent_id;
    }
    return managers[0]?.member_id ?? "";
  }, [managerId, managers, currentUserId, processes]);

  const [activeManagerId, setActiveManagerId] = useState(defaultManagerId);
  useEffect(() => {
    if (defaultManagerId && defaultManagerId !== activeManagerId) {
      setActiveManagerId(defaultManagerId);
    }
  }, [defaultManagerId, activeManagerId]);

  const selectManager = (next: string) => {
    setActiveManagerId(next);
    onManagerChange(next);
  };

  const processQuery = useQuery(channelGoalProcessOptions(channelId, activeManagerId));
  const processDoc = processQuery.data?.process ?? null;
  const activeManager = managers.find((m) => m.member_id === activeManagerId);

  const refresh = () => {
    void processesQuery.refetch();
    void processQuery.refetch();
  };

  const shortStatus = (
    <div className="space-y-2.5 rounded-md border border-border/50 bg-muted/20 px-3 py-2.5">
      <div>
        <div className="text-[9.5px] font-bold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.goal.current_step)}
        </div>
        <p className="mt-0.5 text-sm font-medium leading-snug">
          {goal.current_step?.trim()
            ? goal.current_step
            : <span className="italic text-muted-foreground">{t(($) => $.goal.none)}</span>}
        </p>
      </div>
      <div>
        <div className="text-[9.5px] font-bold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.goal.blocker_label)}
        </div>
        <p
          className={cn(
            "mt-0.5 text-sm leading-snug",
            goal.blocker?.trim() ? "text-destructive font-medium" : "italic text-muted-foreground",
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
          {goal.progress_summary?.trim()
            ? goal.progress_summary
            : <span className="italic">{t(($) => $.goal.none)}</span>}
        </p>
      </div>
    </div>
  );

  let body: ReactNode;
  if (!activeManagerId) {
    body = (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 py-10 text-center">
        <p className="text-sm text-muted-foreground">{t(($) => $.goal.process_empty)}</p>
      </div>
    );
  } else if (processQuery.isPending && !processDoc) {
    body = (
      <div className="space-y-3 p-4">
        {shortStatus}
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-5/6" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  } else if (processQuery.isError) {
    body = (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-10 text-center">
        <p className="text-sm text-destructive">{t(($) => $.goal.process_error)}</p>
        <Button size="sm" variant="outline" onClick={() => void processQuery.refetch()}>
          {t(($) => $.goal.retry)}
        </Button>
      </div>
    );
  } else if (!processDoc || !processDoc.content.trim()) {
    body = (
      <div className="space-y-3 p-4">
        {shortStatus}
        <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed border-border/60 px-4 py-10 text-center">
          <p className="text-sm text-muted-foreground">
            {t(($) => $.goal.process_empty_for, {
              name: activeManager?.display_name || activeManager?.name || "—",
            })}
          </p>
        </div>
      </div>
    );
  } else {
    body = (
      <div className="space-y-3 p-4">
        {shortStatus}
        <div className="flex items-center justify-between gap-2 text-[11px] text-muted-foreground">
          <span>
            {t(($) => $.goal.updated_at, {
              when: timeAgo(processDoc.updated_at),
            })}
          </span>
          <span>v{processDoc.version}</span>
        </div>
        {processQuery.isFetching ? (
          <p className="text-[11px] text-muted-foreground">{t(($) => $.goal.process_updating)}</p>
        ) : null}
        <div className="prose prose-sm dark:prose-invert max-w-none text-sm">
          <Markdown mode="full">{processDoc.content}</Markdown>
        </div>
      </div>
    );
  }

  return (
    <ConversationSidePanelShell
      variant={variant}
      header="bar"
      onClose={onClose}
      closeAriaLabel={t(($) => $.goal.process_close)}
      actions={
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-8"
          aria-label={t(($) => $.goal.process_refresh)}
          onClick={refresh}
        >
          <RefreshCw className={cn("size-4", processQuery.isFetching && "animate-spin")} />
        </Button>
      }
      leading={
        <div className="min-w-0">
          <div className="text-sm font-semibold leading-none">{t(($) => $.goal.process)}</div>
          <p className="mt-1 truncate text-[11px] text-muted-foreground">{goal.title}</p>
        </div>
      }
    >
      <aside
        id={PANEL_ID}
        role="complementary"
        aria-label={t(($) => $.goal.process)}
        data-testid="goal-process-panel"
        className="flex min-h-0 flex-1 flex-col"
      >
        {managers.length >= 2 ? (
          <Tabs
            value={activeManagerId}
            onValueChange={selectManager}
            className="shrink-0 border-b border-border/40 px-2 pt-2"
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
        <div className="min-h-0 flex-1 overflow-y-auto">{body}</div>
      </aside>
    </ConversationSidePanelShell>
  );
}

export const GOAL_PROCESS_PANEL_ID = PANEL_ID;
