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
import { useT } from "../../i18n";
import { isTerminalChannelActiveTask } from "./conversation-activity-tasks";

const STOPPING_ALL_TASKS_ID = "__all__";
const FACE_MAX = 3;

export interface ChannelAgentsLiveCueProps {
  agentCount: number;
  tasks: readonly ChannelActiveTask[];
  stoppingTaskId?: string | null;
  canStop?: boolean;
  onStopTask?: (task: ChannelActiveTask) => void;
  /** Opens the existing Stop-all confirm dialog (LRM-405). */
  onStopAll?: () => void;
  /**
   * `channel` — right Presence Cluster (LRM-581 lock A).
   * `dm` — compact cue beside the peer name (idle → nothing; K=1 no Working chrome).
   */
  variant?: "channel" | "dm";
  memberCount?: number;
  /** Channel roster faces for the Presence Cluster facepile. */
  members?: readonly ChannelMemberBrief[];
  /** Idle cluster click → Members dialog. */
  onOpenMembers?: () => void;
  className?: string;
}

function taskRowKey(task: ChannelActiveTask): string {
  return task.inbox_event_id?.trim() || task.task_id;
}

type FaceItem = {
  key: string;
  memberType: "user" | "agent";
  memberId: string;
  name: string;
  avatarUrl?: string | null;
  working: boolean;
};

/**
 * LRM-581 / lock A v3 — channel header Presence Cluster.
 *
 * K≥2: right facepile + `N · M` idle / `N · M · K working` shimmer; Stop all +
 * row Stop only inside desktop hover / mobile tap card (no outer Stop chrome).
 * K=1: no Working chrome on the header (Activity owns that state).
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

  const { stoppable, terminal, listTasks, runningCount } = useMemo(() => {
    const stop: ChannelActiveTask[] = [];
    const term: ChannelActiveTask[] = [];
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
      if (task.status === "running") running += 1;
    }
    return {
      stoppable: stop,
      terminal: term,
      listTasks: [...stop, ...term],
      runningCount: running,
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
  // K=1: header must not show Working chrome (lock A v3 / AC).
  const allowWorkingChrome = agentsOnly ? false : agentCount >= 2;
  const isLive = allowWorkingChrome && listTasks.length > 0;
  const hasStoppable = stoppable.length > 0;

  const workingAgentIds = useMemo(() => {
    const ids = new Set<string>();
    for (const task of stoppable) ids.add(task.agent_id);
    return ids;
  }, [stoppable]);

  const faces = useMemo((): FaceItem[] => {
    if (agentsOnly) return [];
    const workingFaces: FaceItem[] = [];
    const idleFaces: FaceItem[] = [];
    const seen = new Set<string>();

    for (const task of stoppable) {
      const key = `agent:${task.agent_id}`;
      if (seen.has(key)) continue;
      seen.add(key);
      workingFaces.push({
        key,
        memberType: "agent",
        memberId: task.agent_id,
        name: getActorName("agent", task.agent_id) || task.agent_name,
        avatarUrl: getActorAvatarUrl("agent", task.agent_id),
        working: true,
      });
    }

    for (const m of members) {
      const key = `${m.member_type}:${m.member_id}`;
      if (seen.has(key)) continue;
      seen.add(key);
      const working =
        m.member_type === "agent" && workingAgentIds.has(m.member_id);
      const item: FaceItem = {
        key,
        memberType: m.member_type,
        memberId: m.member_id,
        name: m.display_name || m.name,
        avatarUrl: m.avatar_url,
        working,
      };
      if (working) workingFaces.push(item);
      else idleFaces.push(item);
    }

    return [...workingFaces, ...idleFaces].slice(0, FACE_MAX);
  }, [
    agentsOnly,
    members,
    stoppable,
    workingAgentIds,
    getActorName,
    getActorAvatarUrl,
  ]);

  if (agentsOnly) {
    // K=1 DM: no header Working chrome — Stop lives on profile / Activity (LRM-589).
    return null;
  }

  if (agentCount <= 0 && memberCount <= 0 && listTasks.length === 0) {
    return null;
  }

  const countLabel = isLive
    ? stoppable.length > 0
      ? t(($) => $.header.presence_live, {
          members: memberCount,
          agents: agentCount,
          working: stoppable.length,
        })
      : t(($) => $.header.presence_attention, {
          members: memberCount,
          agents: agentCount,
        })
    : t(($) => $.header.presence_idle, {
        members: memberCount,
        agents: agentCount,
      });

  const clusterAriaLabel = isLive
    ? countLabel
    : t(($) => $.header.view_members_aria);

  const countClass = cn(
    "whitespace-nowrap text-xs font-semibold text-muted-foreground",
    isLive && runningCount > 0 && "animate-chat-text-shimmer",
    isLive &&
      runningCount === 0 &&
      terminal.length > 0 &&
      "text-destructive",
    isLive &&
      runningCount === 0 &&
      terminal.length === 0 &&
      "text-foreground",
  );

  const dismissTerminal = (task: ChannelActiveTask) => {
    const key = taskRowKey(task);
    setDismissedKeys((prev) => {
      if (prev.has(key)) return prev;
      const next = new Set(prev);
      next.add(key);
      return next;
    });
  };

  const facepile = (
    <span className="inline-flex items-center" data-testid="channel-presence-facepile">
      {faces.map((face, i) => (
        <span
          key={face.key}
          className={cn(
            "relative inline-flex rounded-full ring-2 ring-background",
            face.working && "animate-presence-face-enter",
          )}
          style={{
            marginLeft: i === 0 ? 0 : -6,
            animationDelay: face.working ? `${i * 60}ms` : undefined,
          }}
        >
          {face.working ? (
            <span
              aria-hidden
              className="pointer-events-none absolute -inset-0.5 rounded-full border-[1.5px] border-brand animate-presence-working-ring"
            />
          ) : null}
          <ActorAvatarBase
            name={face.name}
            initials={getActorInitials(
              face.memberType === "agent" ? "agent" : "user",
              face.memberId,
            )}
            avatarUrl={
              face.avatarUrl ??
              getActorAvatarUrl(
                face.memberType === "agent" ? "agent" : "user",
                face.memberId,
              )
            }
            isAgent={face.memberType === "agent"}
            size={22}
          />
        </span>
      ))}
    </span>
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
          const firstSeen = firstSeenRef.current!.get(taskRowKey(task));
          const duration =
            !isTerminal && firstSeen
              ? formatDuration(new Date(firstSeen).toISOString(), now)
              : "";
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
              ? "bg-brand animate-ping"
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
              <span className="relative flex size-1.5 shrink-0">
                <span className={cn("absolute inset-0 rounded-full", dotClass)} />
                <span
                  className={cn(
                    "relative size-1.5 rounded-full",
                    isFailed || isNoReply
                      ? "bg-destructive"
                      : isRunning
                        ? "bg-brand"
                        : "bg-muted-foreground/40",
                  )}
                />
              </span>
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
      {hasStoppable && canStop && onStopAll && stoppable.length > 1 ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="mt-1 h-8 w-full justify-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
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
      ) : null}
    </div>
  );

  const clusterInner = (
    <>
      {faces.length > 0 ? facepile : null}
      <span className={countClass} data-testid="channel-presence-count">
        {countLabel}
      </span>
    </>
  );

  const clusterClass = cn(
    "inline-flex min-h-8 min-w-0 max-w-full items-center gap-1.5 rounded-lg py-0.5 pl-1 pr-1.5 text-left outline-none transition-colors",
    "hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring",
    className,
  );

  // Idle / K=1: click opens Members. Live K≥2: hover/tap opens Working list.
  if (!isLive) {
    return (
      <button
        type="button"
        className={clusterClass}
        onClick={onOpenMembers}
        aria-label={clusterAriaLabel}
        data-testid="channel-agents-live-cue"
        data-presence-state="idle"
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
              className={clusterClass}
              data-testid="channel-agents-live-cue"
              data-presence-state="live"
              aria-expanded={mobileOpen}
              aria-label={clusterAriaLabel}
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
            className={clusterClass}
            data-testid="channel-agents-live-cue"
            data-presence-state="live"
            aria-label={clusterAriaLabel}
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
