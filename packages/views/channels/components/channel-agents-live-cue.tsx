"use client";

import { useEffect, useMemo, useRef, useState } from "react";
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
import type { ChannelActiveTask, ChannelMemberBrief } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { formatDuration } from "../../agents/components/agent-activity-hover-content";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n";
import { isTerminalChannelActiveTask } from "./conversation-activity-tasks";

const STOPPING_ALL_TASKS_ID = "__all__";
const FACE_MAX = 3;
const FACE_SIZE = 22;

export interface ChannelAgentsLiveCueProps {
  agentCount: number;
  tasks: readonly ChannelActiveTask[];
  stoppingTaskId?: string | null;
  canStop?: boolean;
  onStopTask?: (task: ChannelActiveTask) => void;
  /** Opens the existing Stop-all confirm dialog (LRM-405). */
  onStopAll?: () => void;
  /**
   * `channel` — right Presence Cluster (faces · members · agents [· K working]).
   * `dm` — compact cue; K=1 has no header Working chrome (Activity / profile).
   */
  variant?: "channel" | "dm";
  memberCount?: number;
  /** Channel roster for the facepile (Presence Cluster). */
  members?: readonly ChannelMemberBrief[];
  /** Idle / K&lt;2 click opens Members (channel only). */
  onOpenMembers?: () => void;
  membersOpen?: boolean;
  className?: string;
}

function taskRowKey(task: ChannelActiveTask): string {
  return task.inbox_event_id?.trim() || task.task_id;
}

function PresenceFaceStack({
  members,
  workingAgentIds,
  showPulse,
}: {
  members: readonly ChannelMemberBrief[];
  workingAgentIds: ReadonlySet<string>;
  showPulse: boolean;
}) {
  const workingFirst = useMemo(() => {
    const working: ChannelMemberBrief[] = [];
    const rest: ChannelMemberBrief[] = [];
    for (const m of members) {
      if (
        m.member_type === "agent" &&
        workingAgentIds.has(m.member_id)
      ) {
        working.push(m);
      } else {
        rest.push(m);
      }
    }
    return [...working, ...rest].slice(0, FACE_MAX);
  }, [members, workingAgentIds]);

  const overlap = Math.round(FACE_SIZE * 0.28);

  return (
    <span className="inline-flex items-center" data-testid="channel-presence-faces">
      {workingFirst.map((m, i) => {
        const isWorkingFace =
          showPulse &&
          m.member_type === "agent" &&
          workingAgentIds.has(m.member_id);
        return (
          <span
            key={`${m.member_type}:${m.member_id}`}
            style={{ marginLeft: i === 0 ? 0 : -overlap }}
            className={cn(
              "relative inline-flex rounded-full ring-2 ring-background",
              isWorkingFace &&
                "after:pointer-events-none after:absolute after:inset-[-3px] after:animate-pulse after:rounded-full after:border-2 after:border-brand/65",
            )}
            data-working={isWorkingFace ? "true" : undefined}
          >
            <ActorAvatar
              actorType={m.member_type === "agent" ? "agent" : "member"}
              actorId={m.member_id}
              size={FACE_SIZE}
              avatarUrlHint={m.avatar_url}
              showStatusDot
              profileLink={false}
            />
          </span>
        );
      })}
    </span>
  );
}

/**
 * LRM-581 / LRM-584 lock A — channel header Presence Cluster.
 *
 * Idle / K&lt;2: faces · members · agents (no Working chrome, no outer Stop).
 * K≥2 working: working faces first + brand pulse + · K working shimmer;
 * desktop hover / mobile tap opens Activity-同源 Working list; Stop all +
 * row Stop only inside the card.
 */
export function ChannelAgentsLiveCue({
  agentCount,
  tasks,
  stoppingTaskId = null,
  canStop = true,
  onStopTask,
  onStopAll,
  variant = "channel",
  memberCount = 0,
  members = [],
  onOpenMembers,
  membersOpen = false,
  className,
}: ChannelAgentsLiveCueProps) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [dismissedKeys, setDismissedKeys] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const firstSeenRef = useRef<Map<string, number> | null>(null);
  if (firstSeenRef.current === null) {
    firstSeenRef.current = new Map();
  }
  const [now, setNow] = useState(() => Date.now());
  const { getActorName, getActorInitials, getActorAvatarUrl } = useActorName();

  const presentKeys = useMemo(
    () => new Set(tasks.map((task) => taskRowKey(task))),
    [tasks],
  );
  const activeDismissed = useMemo(() => {
    const next = new Set<string>();
    for (const key of dismissedKeys) {
      if (presentKeys.has(key)) next.add(key);
    }
    return next;
  }, [dismissedKeys, presentKeys]);

  const { stoppable, terminal, listTasks, runningCount, workingAgentIds } =
    useMemo(() => {
      const stop: ChannelActiveTask[] = [];
      const term: ChannelActiveTask[] = [];
      const workingIds = new Set<string>();
      let running = 0;
      for (const task of tasks) {
        const key = taskRowKey(task);
        if (activeDismissed.has(key)) continue;
        if (isTerminalChannelActiveTask(task)) {
          const outcome = task.outcome?.trim();
          if (outcome === "failed" || outcome === "no_reply") {
            term.push(task);
          }
          continue;
        }
        stop.push(task);
        workingIds.add(task.agent_id);
        if (task.status === "running") running += 1;
      }
      return {
        stoppable: stop,
        terminal: term,
        listTasks: [...stop, ...term],
        runningCount: running,
        workingAgentIds: workingIds,
      };
    }, [tasks, activeDismissed]);

  useEffect(() => {
    const seen = firstSeenRef.current!;
    const liveKeys = new Set<string>();
    for (const task of listTasks) {
      if (isTerminalChannelActiveTask(task)) continue;
      const key = taskRowKey(task);
      liveKeys.add(key);
      if (!seen.has(key)) seen.set(key, Date.now());
    }
    for (const key of [...seen.keys()]) {
      if (!liveKeys.has(key)) seen.delete(key);
    }
  }, [listTasks]);

  useEffect(() => {
    if (runningCount === 0) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [runningCount]);

  const agentsOnly = variant === "dm";
  // Frank v3 / LRM-584: K=1 → no header Working chrome (Activity / profile).
  // Multi-agent overview (K≥2) gets Presence Cluster live + hover Stop card.
  const workingK = stoppable.length;
  const showWorkingChrome = workingK >= 2;
  // Hover/tap card for multi-working OR terminal attention (Dismiss).
  const showWorkingList = showWorkingChrome || terminal.length > 0;
  const hasAttentionOnly =
    !showWorkingChrome && terminal.length > 0 && workingK === 0;

  if (agentsOnly) {
    // DM / single-agent: never invent Working in the header.
    return null;
  }

  if (agentCount <= 0 && memberCount <= 0 && listTasks.length === 0) {
    return null;
  }

  const rosterLabel =
    memberCount > 0 && agentCount > 0
      ? t(($) => $.header.presence_counts, {
          members: memberCount,
          agents: agentCount,
        })
      : memberCount > 0
        ? t(($) => $.header.members_only, { members: memberCount })
        : t(($) => $.header.agents_idle, { agents: agentCount });

  const workingLabel = showWorkingChrome
    ? t(($) => $.header.presence_working, { working: workingK })
    : hasAttentionOnly
      ? t(($) => $.header.presence_attention)
      : null;

  const ariaLabel = workingLabel
    ? `${rosterLabel} · ${workingLabel}`
    : rosterLabel;

  const dismissTerminal = (task: ChannelActiveTask) => {
    const key = taskRowKey(task);
    setDismissedKeys((prev) => {
      if (prev.has(key)) return prev;
      const next = new Set(prev);
      next.add(key);
      return next;
    });
  };

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
          const firstSeen = firstSeenRef.current!.get(taskRowKey(task));
          const duration =
            !isTerminal && firstSeen
              ? formatDuration(new Date(firstSeen).toISOString(), now)
              : "";
          // Activity-同源 verbs (channels.agent_status mirrors Activity labels).
          const verb = isFailed
            ? t(($) => $.header.working_failed)
            : isNoReply
              ? t(($) => $.header.working_no_reply)
              : isRunning
                ? t(($) => $.agent_status.running)
                : t(($) => $.agent_status.queued);
          const verbLine =
            duration && !isTerminal
              ? t(($) => $.header.working_verb_with_duration, {
                  verb,
                  duration,
                })
              : verb;
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
          const rowKey = taskRowKey(task);

          return (
            <div
              key={rowKey}
              className="flex min-h-8 items-center gap-2 rounded-md px-1 py-1 text-xs hover:bg-muted/60"
              data-testid="channel-agents-working-row"
              data-terminal={isTerminal ? "true" : "false"}
            >
              <ActorAvatarBase
                name={getActorName("agent", task.agent_id) || task.agent_name}
                initials={getActorInitials("agent", task.agent_id)}
                avatarUrl={getActorAvatarUrl("agent", task.agent_id)}
                isAgent
                size={22}
              />
              <span
                className={cn(
                  "relative h-1.5 w-1.5 shrink-0 rounded-full",
                  dotClass,
                  isRunning &&
                    "after:absolute after:inset-[-3px] after:animate-ping after:rounded-full after:bg-brand/40",
                )}
              />
              <div className="min-w-0 flex-1">
                <div className="truncate font-semibold text-foreground">
                  {getActorName("agent", task.agent_id) || task.agent_name}
                </div>
                <div className={cn("truncate", verbClass)}>{verbLine}</div>
              </div>
              {isTerminal ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 shrink-0 px-2 text-[11px] text-muted-foreground hover:text-foreground"
                  onClick={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    dismissTerminal(task);
                  }}
                  aria-label={t(($) => $.header.working_dismiss_aria, {
                    name: task.agent_name,
                  })}
                  data-testid="channel-agents-working-dismiss"
                >
                  {actionLabel}
                </Button>
              ) : canStop && onStopTask ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 shrink-0 gap-1 px-2 text-[11px] text-muted-foreground hover:text-foreground"
                  disabled={
                    stoppingTaskId === task.task_id ||
                    stoppingTaskId === STOPPING_ALL_TASKS_ID
                  }
                  onClick={(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    onStopTask(task);
                  }}
                  aria-label={t(($) => $.agent_status.stop_aria, {
                    name: task.agent_name,
                  })}
                  data-testid="channel-agents-working-stop"
                >
                  <Square className="size-2.5 fill-current" />
                  {actionLabel}
                </Button>
              ) : null}
            </div>
          );
        })}
      </div>
      {showWorkingList &&
      canStop &&
      onStopAll &&
      stoppable.length > 1 ? (
        <div className="border-t border-border/50 pt-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 w-full justify-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
            disabled={stoppingTaskId === STOPPING_ALL_TASKS_ID}
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onStopAll();
            }}
            aria-label={t(($) => $.agent_status.stop_all_aria, {
              count: stoppable.length,
            })}
            data-testid="channel-agents-working-stop-all"
          >
            <Square className="size-2.5 fill-current" />
            {t(($) => $.agent_status.stop_all)}
          </Button>
        </div>
      ) : null}
    </div>
  );

  const clusterInner = (
    <>
      <PresenceFaceStack
        members={members}
        workingAgentIds={workingAgentIds}
        showPulse={showWorkingChrome && runningCount > 0}
      />
      <span className="inline-flex min-w-0 flex-col items-start leading-tight">
        <span
          className="truncate text-xs font-semibold text-muted-foreground"
          data-testid="channel-presence-counts"
        >
          {rosterLabel}
        </span>
        {workingLabel ? (
          <span
            className={cn(
              "truncate text-[11px] font-semibold",
              hasAttentionOnly
                ? "text-destructive"
                : "animate-chat-text-shimmer text-foreground",
            )}
            data-testid="channel-presence-working"
          >
            {workingLabel}
          </span>
        ) : null}
      </span>
    </>
  );

  const clusterButtonClass = cn(
    "inline-flex min-h-8 min-w-0 items-center gap-1.5 rounded-md py-0 pl-1 pr-1.5 text-left text-foreground transition-colors outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring",
    membersOpen && !showWorkingList && "bg-muted",
  );

  // Idle / K&lt;2: click opens Members. K≥2: hover/tap opens Working list.
  if (!showWorkingList) {
    return (
      <button
        type="button"
        onClick={onOpenMembers}
        className={cn(clusterButtonClass, className)}
        aria-label={
          onOpenMembers
            ? t(($) => $.header.view_members_aria)
            : ariaLabel
        }
        data-testid="channel-agents-live-cue"
        data-presence="idle"
      >
        {clusterInner}
      </button>
    );
  }

  if (isMobile) {
    return (
      <Popover open={mobileOpen} onOpenChange={setMobileOpen}>
        <PopoverTrigger
          render={
            <button
              type="button"
              className={cn(clusterButtonClass, className)}
              data-testid="channel-agents-live-cue"
              data-presence="working"
              aria-expanded={mobileOpen}
              aria-label={ariaLabel}
            >
              {clusterInner}
            </button>
          }
        />
        <PopoverContent align="end" className="w-72 p-3">
          {listBody}
        </PopoverContent>
      </Popover>
    );
  }

  return (
    <HoverCard>
      <HoverCardTrigger
        render={
          <button
            type="button"
            className={cn(clusterButtonClass, className)}
            data-testid="channel-agents-live-cue"
            data-presence="working"
            aria-label={ariaLabel}
          >
            {clusterInner}
          </button>
        }
      />
      <HoverCardContent align="end" className="w-72 p-3">
        {listBody}
      </HoverCardContent>
    </HoverCard>
  );
}
