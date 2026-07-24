"use client";

import { useMemo, useState } from "react";
import { Square } from "lucide-react";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Button } from "@multica/ui/components/ui/button";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@multica/ui/components/ui/hover-card";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ChannelActiveTask } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import {
  filterComposerStripTasks,
  isTerminalChannelActiveTask,
} from "./conversation-activity-tasks";

const STOPPING_ALL_TASKS_ID = "__all__";

export interface ChannelAgentsLiveCueProps {
  agentCount: number;
  tasks: readonly ChannelActiveTask[];
  stoppingTaskId?: string | null;
  canStop?: boolean;
  onStopTask?: (task: ChannelActiveTask) => void;
  /** Opens the existing Stop-all confirm dialog (LRM-405). */
  onStopAll?: () => void;
  /** When true, render only the agents cue (no leading members text). */
  agentsOnly?: boolean;
  memberCount?: number;
  className?: string;
}

/**
 * LRM-581 / lock E — channel header `N agents` live cue + Working list.
 *
 * Idle: plain "N agents" (no chrome). With active/terminal tasks: cue changes
 * ("N agents · K processing" + shimmer when running); Stop / Stop all stay
 * visible next to the cue (not hover-only). Desktop hover / mobile tap opens
 * the Working list (avatar + dot + verb + per-row Stop / dismiss).
 */
export function ChannelAgentsLiveCue({
  agentCount,
  tasks,
  stoppingTaskId = null,
  canStop = true,
  onStopTask,
  onStopAll,
  agentsOnly = false,
  memberCount = 0,
  className,
}: ChannelAgentsLiveCueProps) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const [mobileOpen, setMobileOpen] = useState(false);
  const { getActorName, getActorInitials, getActorAvatarUrl } = useActorName();

  const { stoppable, terminal, listTasks, runningCount } = useMemo(() => {
    const scoped = filterComposerStripTasks(tasks);
    const stop: ChannelActiveTask[] = [];
    const term: ChannelActiveTask[] = [];
    let running = 0;
    for (const task of scoped) {
      if (isTerminalChannelActiveTask(task)) {
        const outcome = task.outcome?.trim();
        if (outcome === "failed" || outcome === "no_reply") {
          term.push(task);
        }
        continue;
      }
      stop.push(task);
      if (task.status === "running") running += 1;
    }
    return {
      stoppable: stop,
      terminal: term,
      listTasks: [...stop, ...term],
      runningCount: running,
    };
  }, [tasks]);

  if (agentCount <= 0 && listTasks.length === 0) {
    if (agentsOnly) return null;
    if (memberCount <= 0) return null;
    return (
      <span className={cn("truncate", className)} data-testid="channel-roster-summary">
        {t(($) => $.header.members_only, { members: memberCount })}
      </span>
    );
  }

  // DM peer cue (agentsOnly): idle = no chrome under the name (peer already
  // identified). Channel roster still shows the idle "N agents" label.
  if (agentsOnly && listTasks.length === 0) {
    return null;
  }

  const isLive = listTasks.length > 0;
  const hasStoppable = stoppable.length > 0;
  const agentsLabel = isLive
    ? stoppable.length > 0
      ? t(($) => $.header.agents_live, {
          agents: agentCount,
          working: stoppable.length,
        })
      : t(($) => $.header.agents_attention, { agents: agentCount })
    : t(($) => $.header.agents_idle, { agents: agentCount });

  const membersPrefix =
    !agentsOnly && memberCount > 0
      ? t(($) => $.header.members_prefix, { members: memberCount })
      : null;

  const cueClass = cn(
    "inline-flex min-h-8 min-w-0 items-center rounded-sm px-0.5 text-left",
    isLive && "outline-none focus-visible:ring-2 focus-visible:ring-ring",
    isLive && runningCount > 0 && "animate-chat-text-shimmer font-semibold text-foreground",
    isLive && runningCount === 0 && terminal.length > 0 && "font-semibold text-destructive",
    isLive && runningCount === 0 && terminal.length === 0 && "font-semibold text-foreground",
  );

  const listBody = (
    <div className="flex flex-col gap-2" data-testid="channel-agents-working-list">
      <div className="text-xs font-medium text-muted-foreground">
        {t(($) => $.header.working_list_title, { count: listTasks.length })}
      </div>
      <div className="flex flex-col gap-1">
        {listTasks.map((task) => {
          const isTerminal = isTerminalChannelActiveTask(task);
          const outcome = task.outcome?.trim();
          const isFailed = outcome === "failed";
          const isNoReply = outcome === "no_reply";
          const isRunning = task.status === "running";
          const verb = isFailed
            ? t(($) => $.header.working_failed)
            : isNoReply
              ? t(($) => $.header.working_no_reply)
              : isRunning
                ? t(($) => $.agent_status.running)
                : t(($) => $.agent_status.queued);
          const dotClass = isFailed || isNoReply
            ? "bg-destructive"
            : isRunning
              ? "bg-brand"
              : "bg-muted-foreground/40";
          const verbClass =
            isFailed || isNoReply ? "text-destructive" : "text-muted-foreground";
          const actionLabel = isTerminal
            ? t(($) => $.header.working_dismiss)
            : t(($) => $.agent_status.stop);
          const rowKey = task.inbox_event_id ?? task.task_id;

          return (
            <div
              key={rowKey}
              className="flex min-h-8 items-center gap-2 rounded-md px-1 py-1 text-xs hover:bg-muted/60"
              data-testid="channel-agents-working-row"
            >
              <ActorAvatarBase
                name={getActorName("agent", task.agent_id) || task.agent_name}
                initials={getActorInitials("agent", task.agent_id)}
                avatarUrl={getActorAvatarUrl("agent", task.agent_id)}
                isAgent
                size={22}
              />
              <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", dotClass)} />
              <div className="min-w-0 flex-1">
                <div className="truncate font-semibold text-foreground">
                  {getActorName("agent", task.agent_id) || task.agent_name}
                </div>
                <div className={cn("truncate", verbClass)}>{verb}</div>
              </div>
              {canStop && onStopTask ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 shrink-0 px-2 text-[11px] text-muted-foreground hover:text-foreground"
                  disabled={
                    stoppingTaskId === task.task_id ||
                    stoppingTaskId === STOPPING_ALL_TASKS_ID
                  }
                  onClick={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    onStopTask(task);
                  }}
                  aria-label={
                    isTerminal
                      ? t(($) => $.header.working_dismiss_aria, { name: task.agent_name })
                      : t(($) => $.agent_status.stop_aria, { name: task.agent_name })
                  }
                >
                  {!isTerminal ? <Square className="size-2.5 fill-current" /> : null}
                  {actionLabel}
                </Button>
              ) : null}
            </div>
          );
        })}
      </div>
    </div>
  );

  const cueWithHover = !isLive ? (
    <span className="truncate" data-testid="channel-agents-live-cue">
      {agentsLabel}
    </span>
  ) : isMobile ? (
    <Popover open={mobileOpen} onOpenChange={setMobileOpen}>
      <PopoverTrigger
        render={
          <button
            type="button"
            className={cueClass}
            data-testid="channel-agents-live-cue"
            aria-expanded={mobileOpen}
          >
            <span className="truncate">{agentsLabel}</span>
          </button>
        }
      />
      <PopoverContent align="start" className="w-72 p-3">
        {listBody}
      </PopoverContent>
    </Popover>
  ) : (
    <HoverCard>
      <HoverCardTrigger
        render={
          <button
            type="button"
            className={cueClass}
            data-testid="channel-agents-live-cue"
          >
            <span className="truncate">{agentsLabel}</span>
          </button>
        }
      />
      <HoverCardContent align="start" className="w-72 p-3">
        {listBody}
      </HoverCardContent>
    </HoverCard>
  );

  return (
    <span
      className={cn(
        "inline-flex min-w-0 max-w-full flex-wrap items-center gap-x-1.5 gap-y-1",
        className,
      )}
      data-testid="channel-roster-summary"
    >
      {membersPrefix ? <span className="shrink-0">{membersPrefix}</span> : null}
      {cueWithHover}
      {isLive && hasStoppable && canStop && onStopTask && stoppable.length === 1 ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-8 shrink-0 gap-1 px-2 text-[11px] text-muted-foreground hover:text-foreground"
          disabled={
            stoppingTaskId === stoppable[0]!.task_id ||
            stoppingTaskId === STOPPING_ALL_TASKS_ID
          }
          onClick={() => onStopTask(stoppable[0]!)}
          aria-label={t(($) => $.agent_status.stop_aria, {
            name: stoppable[0]!.agent_name,
          })}
          data-testid="channel-agents-cue-stop"
        >
          <Square className="size-2.5 fill-current" />
          {t(($) => $.agent_status.stop)}
        </Button>
      ) : null}
      {isLive && hasStoppable && canStop && onStopAll && stoppable.length > 1 ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-8 shrink-0 gap-1 px-2 text-[11px] text-muted-foreground hover:text-foreground"
          disabled={stoppingTaskId === STOPPING_ALL_TASKS_ID}
          onClick={onStopAll}
          aria-label={t(($) => $.agent_status.stop_all_aria, {
            count: stoppable.length,
          })}
          data-testid="channel-agents-cue-stop-all"
        >
          <Square className="size-2.5 fill-current" />
          {t(($) => $.agent_status.stop_all)}
        </Button>
      ) : null}
    </span>
  );
}
