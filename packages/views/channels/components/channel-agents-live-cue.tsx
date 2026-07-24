"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Square } from "lucide-react";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { Button } from "@multica/ui/components/ui/button";
import { ActorAvatar } from "../../common/actor-avatar";
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
import { useWorkspaceId } from "@multica/core/hooks";
import { useActorName } from "@multica/core/workspace/hooks";
import type { ChannelActiveTask, ChannelMemberBrief } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { formatDuration } from "../../agents/components/agent-activity-hover-content";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";
import { useT } from "../../i18n";
import { isTerminalChannelActiveTask } from "./conversation-activity-tasks";

const STOPPING_ALL_TASKS_ID = "__all__";
const FACE_MAX = 3;
const FACE_SIZE = 22;
/** Stable empty default — avoid `members = []` recreating an array each render. */
const EMPTY_MEMBERS: readonly ChannelMemberBrief[] = [];

export interface ChannelPresenceClusterProps {
  members: readonly ChannelMemberBrief[];
  memberCount: number;
  /** Channel agent count (K). Working chrome only when K ≥ 2. */
  agentCount: number;
  tasks: readonly ChannelActiveTask[];
  stoppingTaskId?: string | null;
  canStop?: boolean;
  onStopTask?: (task: ChannelActiveTask) => void;
  /** Opens the existing Stop-all confirm dialog (LRM-405); only from card footer. */
  onStopAll?: () => void;
  /** Click opens Members (idle always; desktop also when working). */
  onOpenMembers?: () => void;
  className?: string;
}

function taskRowKey(task: ChannelActiveTask): string {
  return task.inbox_event_id?.trim() || task.task_id;
}

/**
 * Working-list verb + dot from Agent Activity projection (LRM-581 A v3).
 * Falls back to channel Thinking/Queued only when projection has nothing yet
 * (same Activity opener language — not a second pending semantic).
 */
function useWorkingRowActivityVerb(
  agentId: string,
  task: ChannelActiveTask,
  now: number,
  firstSeen: number | undefined,
) {
  const { t } = useT("channels");
  const wsId = useWorkspaceId();
  const projection = useAgentActivityProjection(wsId, agentId);
  const isTerminal = isTerminalChannelActiveTask(task);
  const outcome = task.outcome?.trim();

  if (outcome === "failed") {
    return {
      verb: t(($) => $.header.working_failed),
      verbClass: "text-destructive",
      dotClass: "bg-destructive",
      ping: false,
    };
  }
  if (outcome === "no_reply") {
    return {
      verb: t(($) => $.header.working_no_reply),
      verbClass: "text-destructive",
      dotClass: "bg-destructive",
      ping: false,
    };
  }

  const duration =
    !isTerminal && firstSeen
      ? formatDuration(new Date(firstSeen).toISOString(), now)
      : "";

  if (projection) {
    const verb = duration
      ? t(($) => $.header.working_verb_with_duration, {
          verb: projection.label,
          duration,
        })
      : projection.label;
    return {
      verb,
      verbClass: "text-muted-foreground",
      dotClass: projection.dotClass || "bg-brand",
      ping: task.status === "running",
    };
  }

  const base =
    task.status === "running"
      ? t(($) => $.agent_status.running)
      : t(($) => $.agent_status.queued);
  const verb = duration
    ? t(($) => $.header.working_verb_with_duration, { verb: base, duration })
    : base;
  return {
    verb,
    verbClass: "text-muted-foreground",
    dotClass: task.status === "running" ? "bg-brand" : "bg-muted-foreground/40",
    ping: task.status === "running",
  };
}

function WorkingListRow({
  task,
  now,
  firstSeen,
  canStop,
  stoppingTaskId,
  onStopTask,
  onDismiss,
}: {
  task: ChannelActiveTask;
  now: number;
  firstSeen: number | undefined;
  canStop: boolean;
  stoppingTaskId: string | null;
  onStopTask?: (task: ChannelActiveTask) => void;
  onDismiss: (task: ChannelActiveTask) => void;
}) {
  const { t } = useT("channels");
  const { getActorName, getActorInitials, getActorAvatarUrl } = useActorName();
  const isTerminal = isTerminalChannelActiveTask(task);
  const { verb, verbClass, dotClass, ping } = useWorkingRowActivityVerb(
    task.agent_id,
    task,
    now,
    firstSeen,
  );
  const actionLabel = isTerminal
    ? t(($) => $.header.working_dismiss)
    : t(($) => $.agent_status.stop);

  return (
    <div
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
      <span className="relative inline-flex h-1.5 w-1.5 shrink-0">
        {ping ? (
          <span
            aria-hidden
            className={cn(
              "absolute inline-flex h-full w-full animate-ping rounded-full opacity-60 motion-reduce:hidden",
              dotClass,
            )}
          />
        ) : null}
        <span className={cn("relative h-1.5 w-1.5 rounded-full", dotClass)} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-semibold text-foreground">
          {getActorName("agent", task.agent_id) || task.agent_name}
        </div>
        <div className={cn("truncate", verbClass)}>{verb}</div>
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
            onDismiss(task);
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
}

/**
 * LRM-581 lock A v3 — right-rail Presence Cluster.
 *
 * - K=1: faces · N · M only; no Working chrome / list / outer Stop (Activity owns work).
 * - K≥2 idle: faces · N · M; click → Members.
 * - K≥2 working: working faces first + brand breathing ring + · K working shimmer;
 *   desktop HoverCard / mobile Popover (≥32px) → Working list; Stop only inside card.
 */
export function ChannelPresenceCluster({
  members,
  memberCount,
  agentCount,
  tasks,
  stoppingTaskId = null,
  canStop = true,
  onStopTask,
  onStopAll,
  onOpenMembers,
  className,
}: ChannelPresenceClusterProps) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const [mobileOpen, setMobileOpen] = useState(false);
  const firstSeenRef = useRef<Map<string, number> | null>(null);
  if (firstSeenRef.current === null) {
    firstSeenRef.current = new Map();
  }
  const [now, setNow] = useState(() => Date.now());

  // Working chrome = live Activity work only (Frank 2026-07-24): never fold
  // terminal `no_reply` / `failed` into the header Working list — those stay
  // on Activity / composer surfaces.
  const { stoppable, listTasks, runningCount, workingAgentIds } = useMemo(() => {
    const stop: ChannelActiveTask[] = [];
    const workingIds = new Set<string>();
    let running = 0;
    for (const task of tasks) {
      if (isTerminalChannelActiveTask(task)) continue;
      stop.push(task);
      workingIds.add(task.agent_id);
      if (task.status === "running") running += 1;
    }
    return {
      stoppable: stop,
      listTasks: stop,
      runningCount: running,
      workingAgentIds: workingIds,
    };
  }, [tasks]);

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

  // K=1: never invent Working chrome — Activity owns single-agent work.
  const allowWorkingChrome = agentCount >= 2;
  const isLive = allowWorkingChrome && listTasks.length > 0;
  const hasStoppable = stoppable.length > 0;
  const workingCount = stoppable.length;

  const stackedMembers = useMemo(() => {
    if (!isLive || workingAgentIds.size === 0) {
      return members.slice(0, FACE_MAX);
    }
    const working: ChannelMemberBrief[] = [];
    const rest: ChannelMemberBrief[] = [];
    for (const m of members) {
      if (m.member_type === "agent" && workingAgentIds.has(m.member_id)) {
        working.push(m);
      } else {
        rest.push(m);
      }
    }
    return [...working, ...rest].slice(0, FACE_MAX);
  }, [members, isLive, workingAgentIds]);

  const countsIdle = t(($) => $.header.presence_counts, {
    members: memberCount,
    agents: agentCount,
  });
  const workingLabel =
    isLive && workingCount > 0
      ? t(($) => $.header.presence_working, { working: workingCount })
      : null;

  const ariaLabel = workingLabel
    ? `${countsIdle} · ${workingLabel}`
    : countsIdle;

  const listBody = (
    <div className="flex flex-col gap-2" data-testid="channel-agents-working-list">
      <div className="text-xs font-medium text-muted-foreground">
        {t(($) => $.header.working_list_title, { count: listTasks.length })}
      </div>
      <div className="flex flex-col gap-1">
        {listTasks.map((task) => (
          <WorkingListRow
            key={taskRowKey(task)}
            task={task}
            now={now}
            firstSeen={firstSeenRef.current!.get(taskRowKey(task))}
            canStop={canStop}
            stoppingTaskId={stoppingTaskId}
            onStopTask={onStopTask}
            onDismiss={() => {
              /* live rows use Stop; terminal outcomes are not listed (Activity SoT) */
            }}
          />
        ))}
      </div>
      {hasStoppable && canStop && onStopAll && stoppable.length > 1 ? (
        <div className="flex items-center justify-end border-t border-border/60 pt-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-8 gap-1 px-2 text-[11px] text-muted-foreground hover:text-foreground"
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

  const overlap = Math.round(FACE_SIZE * 0.28);
  const faceStack = (
    <span
      className="relative isolate inline-flex items-center"
      data-testid="channel-presence-faces"
    >
      {stackedMembers.map((m, i) => {
        const isWorkingFace =
          isLive &&
          m.member_type === "agent" &&
          workingAgentIds.has(m.member_id);
        return (
          <span
            key={`${m.member_type}:${m.member_id}`}
            style={{
              marginLeft: i === 0 ? 0 : -overlap,
              zIndex: i + 1,
              animationDelay: isWorkingFace ? `${i * 60}ms` : undefined,
            }}
            className={cn(
              // Separator ring only when idle — working faces use the brand
              // breathing ring alone so overlaps don't double-stroke.
              "relative inline-flex rounded-full",
              !isWorkingFace && "ring-2 ring-background",
              isWorkingFace &&
                "animate-[presence-face-enter_0.42s_cubic-bezier(0.2,0.8,0.2,1)_both]",
            )}
          >
            <ActorAvatar
              actorType={m.member_type === "agent" ? "agent" : "member"}
              actorId={m.member_id}
              size={FACE_SIZE}
              avatarUrlHint={m.avatar_url}
              // Dense facepile: status-dot punch-outs collide with neighbor rings
              // (Frank 2026-07-24 shot). Working is signaled by brand ring only.
              showStatusDot={false}
              profileLink={false}
            />
            {isWorkingFace ? (
              <span
                aria-hidden
                className="pointer-events-none absolute inset-[-2px] rounded-full border-[1.5px] border-brand motion-reduce:animate-none animate-[presence-ring-pulse_1.6s_ease-in-out_infinite]"
              />
            ) : null}
          </span>
        );
      })}
    </span>
  );

  const countText = (
    <span
      className="inline-flex items-center gap-1.5 text-xs font-semibold text-muted-foreground"
      data-testid="channel-presence-counts"
    >
      {/* Idle roster only — never chain `N · M · K working` as one middot row
          (UI Designer lock 2026-07-24: faces · N · M + independent K working). */}
      <span>{countsIdle}</span>
      {workingLabel ? (
        <span
          className={cn(
            "min-w-0",
            runningCount > 0 && "animate-chat-text-shimmer font-semibold",
            runningCount === 0 && "font-semibold text-foreground",
          )}
          data-testid="channel-presence-working"
        >
          {workingLabel}
        </span>
      ) : null}
    </span>
  );

  const triggerClass = cn(
    // ≥32px touch; no chip chrome (border/bg/pill) per LRM-587.
    "inline-flex min-h-8 min-w-8 items-center gap-1.5 rounded-md py-0.5 pl-1 pr-1.5 text-foreground outline-none transition-colors",
    "hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring",
  );

  const openMembers = () => {
    onOpenMembers?.();
  };

  // Idle (or K=1): plain button → Members. No Working list.
  if (!isLive) {
    return (
      <button
        type="button"
        onClick={openMembers}
        className={cn(triggerClass, className)}
        aria-label={t(($) => $.header.view_members_aria)}
        data-testid="channel-header-members-chip"
        data-presence-working="false"
      >
        {faceStack}
        {countText}
      </button>
    );
  }

  // Mobile working: tap → Working list (≥32px). Members stays via More / details.
  if (isMobile) {
    return (
      <Popover open={mobileOpen} onOpenChange={setMobileOpen}>
        <PopoverTrigger
          render={
            <button
              type="button"
              className={cn(triggerClass, className)}
              aria-expanded={mobileOpen}
              aria-label={ariaLabel}
              data-testid="channel-header-members-chip"
              data-presence-working="true"
            >
              {faceStack}
              {countText}
            </button>
          }
        />
        <PopoverContent align="end" className="w-72 p-3">
          {listBody}
        </PopoverContent>
      </Popover>
    );
  }

  // Desktop working: hover → list; click → Members.
  return (
    <HoverCard>
      <HoverCardTrigger
        render={
          <button
            type="button"
            className={cn(triggerClass, className)}
            aria-label={ariaLabel}
            data-testid="channel-header-members-chip"
            data-presence-working="true"
            onClick={(e) => {
              e.preventDefault();
              openMembers();
            }}
          >
            {faceStack}
            {countText}
          </button>
        }
      />
      <HoverCardContent align="end" className="w-72 p-3">
        {listBody}
      </HoverCardContent>
    </HoverCard>
  );
}

/** @deprecated Prefer ChannelPresenceCluster for channel headers (lock A v3). */
export interface ChannelAgentsLiveCueProps {
  agentCount: number;
  tasks: readonly ChannelActiveTask[];
  stoppingTaskId?: string | null;
  canStop?: boolean;
  onStopTask?: (task: ChannelActiveTask) => void;
  onStopAll?: () => void;
  /**
   * `channel` — Presence Cluster (requires members/memberCount via
   * ChannelPresenceCluster). Prefer calling ChannelPresenceCluster directly.
   * `dm` — compact status beside the peer name (LRM-589; idle → null).
   */
  variant?: "channel" | "dm";
  members?: readonly ChannelMemberBrief[];
  memberCount?: number;
  onOpenMembers?: () => void;
  className?: string;
}

/**
 * LRM-589 DM status cue (Stop stays in AgentProfileActions).
 * Kept as ChannelAgentsLiveCue so merged DM header wiring keeps working after
 * LRM-581 rewrites the channel surface to Presence Cluster.
 */
function DmAgentsLiveCue({
  tasks,
  canStop = true,
  onStopTask,
  onStopAll,
  stoppingTaskId = null,
  className,
}: {
  tasks: readonly ChannelActiveTask[];
  canStop?: boolean;
  onStopTask?: (task: ChannelActiveTask) => void;
  onStopAll?: () => void;
  stoppingTaskId?: string | null;
  className?: string;
}) {
  const { t } = useT("channels");
  const isMobile = useIsMobile();
  const [mobileOpen, setMobileOpen] = useState(false);
  const firstSeenRef = useRef<Map<string, number> | null>(null);
  if (firstSeenRef.current === null) {
    firstSeenRef.current = new Map();
  }
  const [now, setNow] = useState(() => Date.now());

  const { stoppable, listTasks, runningCount } = useMemo(() => {
    const stop: ChannelActiveTask[] = [];
    let running = 0;
    for (const task of tasks) {
      // Match Presence Cluster: terminal no_reply/failed stay on Activity, not Working.
      if (isTerminalChannelActiveTask(task)) continue;
      stop.push(task);
      if (task.status === "running") running += 1;
    }
    return {
      stoppable: stop,
      listTasks: stop,
      runningCount: running,
    };
  }, [tasks]);

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

  if (listTasks.length === 0) return null;

  const hasStoppable = stoppable.length > 0;
  // 1:1 DM — show the status word only (no "1 working" count clutter).
  const agentsLabel = t(($) => $.header.dm_live);

  const cueTextClass = cn(
    "truncate",
    runningCount > 0 && "animate-chat-text-shimmer font-medium",
    runningCount === 0 && "font-medium text-muted-foreground",
  );

  // Compact pill so the cue reads as status chrome, not a second name string.
  const cueButtonClass =
    "inline-flex min-h-8 shrink-0 items-center rounded-md bg-muted/60 px-1.5 py-0.5 text-left text-xs text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring";

  const listBody = (
    <div className="flex flex-col gap-2" data-testid="channel-agents-working-list">
      <div className="text-xs font-medium text-muted-foreground">
        {t(($) => $.header.working_list_title, { count: listTasks.length })}
      </div>
      <div className="flex flex-col gap-1">
        {listTasks.map((task) => (
          <WorkingListRow
            key={taskRowKey(task)}
            task={task}
            now={now}
            firstSeen={firstSeenRef.current!.get(taskRowKey(task))}
            canStop={canStop}
            stoppingTaskId={stoppingTaskId}
            onStopTask={onStopTask}
            onDismiss={() => {
              /* live rows use Stop; terminal outcomes are not listed */
            }}
          />
        ))}
      </div>
    </div>
  );

  const cueWithHover = isMobile ? (
    <Popover open={mobileOpen} onOpenChange={setMobileOpen}>
      <PopoverTrigger
        render={
          <button
            type="button"
            className={cueButtonClass}
            data-testid="channel-agents-live-cue"
            aria-expanded={mobileOpen}
            aria-label={agentsLabel}
          >
            <span className={cueTextClass}>{agentsLabel}</span>
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
            className={cueButtonClass}
            data-testid="channel-agents-live-cue"
            aria-label={agentsLabel}
          >
            <span className={cueTextClass}>{agentsLabel}</span>
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
        "inline-flex shrink-0 items-center gap-x-1.5 gap-y-1",
        className,
      )}
      data-testid="channel-roster-summary"
    >
      {cueWithHover}
      {hasStoppable && canStop && onStopTask && stoppable.length === 1 ? (
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
      {hasStoppable && canStop && onStopAll && stoppable.length > 1 ? (
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

/**
 * Compatibility export: DM uses compact status cue; channel callers should use
 * ChannelPresenceCluster directly (channels-page already does).
 */
export function ChannelAgentsLiveCue({
  variant = "channel",
  agentCount,
  tasks,
  stoppingTaskId,
  canStop,
  onStopTask,
  onStopAll,
  members = EMPTY_MEMBERS,
  memberCount = 0,
  onOpenMembers,
  className,
}: ChannelAgentsLiveCueProps) {
  if (variant === "dm") {
    return (
      <DmAgentsLiveCue
        tasks={tasks}
        canStop={canStop}
        onStopTask={onStopTask}
        onStopAll={onStopAll}
        stoppingTaskId={stoppingTaskId}
        className={className}
      />
    );
  }
  return (
    <ChannelPresenceCluster
      members={members}
      memberCount={memberCount}
      agentCount={agentCount}
      tasks={tasks}
      stoppingTaskId={stoppingTaskId}
      canStop={canStop}
      onStopTask={onStopTask}
      onStopAll={onStopAll}
      onOpenMembers={onOpenMembers}
      className={className}
    />
  );
}
