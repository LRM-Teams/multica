"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useQueries } from "@tanstack/react-query";
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
import { memberProfileOptions } from "@multica/core/workspace/queries";
import {
  directoryActorDisplayName,
  isDirectoryActorMiss,
  profileActorDisplayName,
} from "@multica/core/workspace/resolved-actor-name";
import type { ChannelActiveTask, ChannelMemberBrief } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { formatDuration } from "../../agents/components/agent-activity-hover-content";
import { useAgentActivityProjection } from "../../agents/use-agent-live-status";
import { useT } from "../../i18n";
import { isTerminalChannelActiveTask } from "./conversation-activity-tasks";

/**
 * LRM-391 / UI Designer 2026-07-25: emit-time `agent_name` is usable only when
 * it is a real display label — never the directory-miss sentinel.
 */
function taskEmitDisplayName(agentName: string | undefined): string | null {
  const trimmed = agentName?.trim() ?? "";
  return isDirectoryActorMiss(trimmed) ? null : trimmed;
}

const STOPPING_ALL_TASKS_ID = "__all__";
const FACE_MAX = 3;
const FACE_SIZE = 22;

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
  displayName,
  now,
  firstSeen,
  canStop,
  stoppingTaskId,
  onStopTask,
  onDismiss,
}: {
  task: ChannelActiveTask;
  /** Resolved live label — never "Unknown Agent" (LRM-391). */
  displayName: string;
  now: number;
  firstSeen: number | undefined;
  canStop: boolean;
  stoppingTaskId: string | null;
  onStopTask?: (task: ChannelActiveTask) => void;
  onDismiss: (task: ChannelActiveTask) => void;
}) {
  const { t } = useT("channels");
  const { getActorInitials, getActorAvatarUrl } = useActorName();
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
        name={displayName}
        initials={
          /[a-z]/i.test(displayName.charAt(0))
            ? displayName.charAt(0).toUpperCase()
            : displayName.charAt(0) || getActorInitials("agent", task.agent_id)
        }
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
        <div className="truncate font-semibold text-foreground">{displayName}</div>
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
            name: displayName,
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
            name: displayName,
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
 * - K=1: faces only; no Working chrome / list / outer Stop (Activity owns work).
 * - K≥2 idle: silent facepile (no N/M count text); click → Members.
 * - K≥2 working: working faces first + brand breathing ring (no outer count /
 *   "K working" text); desktop HoverCard / mobile Popover (≥32px) → Working list;
 *   Stop only inside card. (Frank + UI Designer 2026-07-24: outer text gone.)
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
  const wsId = useWorkspaceId();
  const { getActorName } = useActorName();
  const [mobileOpen, setMobileOpen] = useState(false);
  const firstSeenRef = useRef<Map<string, number> | null>(null);
  if (firstSeenRef.current === null) {
    firstSeenRef.current = new Map();
  }
  const [now, setNow] = useState(() => Date.now());

  // Live (non-terminal) tasks — Working chrome source before identity filter.
  const liveTasks = useMemo(
    () => tasks.filter((task) => !isTerminalChannelActiveTask(task)),
    [tasks],
  );

  // LRM-391: ListAgents can miss channel/private / group-manager agents, so
  // `getActorName` alone returns "Unknown Agent". Resolve directory → emit-time
  // name → member-profile; UI Designer 2026-07-25: unresolved rows stay out of
  // the Working list (no sentinel fallback — LRM-238).
  const uniqueLiveAgentIds = useMemo(() => {
    const ids: string[] = [];
    const seen = new Set<string>();
    for (const task of liveTasks) {
      if (!task.agent_id || seen.has(task.agent_id)) continue;
      seen.add(task.agent_id);
      ids.push(task.agent_id);
    }
    return ids;
  }, [liveTasks]);

  const emitNamesByAgent = useMemo(() => {
    const map = new Map<string, string>();
    for (const task of liveTasks) {
      if (map.has(task.agent_id)) continue;
      const emit = taskEmitDisplayName(task.agent_name);
      if (emit) map.set(task.agent_id, emit);
    }
    return map;
  }, [liveTasks]);

  const directoryNamesByAgent = useMemo(() => {
    const map = new Map<string, string>();
    for (const agentId of uniqueLiveAgentIds) {
      const name = directoryActorDisplayName(getActorName, "agent", agentId);
      if (name) map.set(agentId, name);
    }
    return map;
  }, [uniqueLiveAgentIds, getActorName]);

  const profileMissIds = useMemo(
    () =>
      uniqueLiveAgentIds.filter(
        (agentId) =>
          !directoryNamesByAgent.has(agentId) && !emitNamesByAgent.has(agentId),
      ),
    [uniqueLiveAgentIds, directoryNamesByAgent, emitNamesByAgent],
  );

  const profileQueries = useQueries({
    queries: profileMissIds.map((agentId) => ({
      ...memberProfileOptions(wsId, "agent", agentId),
      enabled: !!wsId && !!agentId,
    })),
  });

  const profileNamesByAgent = useMemo(() => {
    const map = new Map<string, string>();
    profileMissIds.forEach((agentId, index) => {
      const name = profileActorDisplayName(profileQueries[index]?.data);
      if (name) map.set(agentId, name);
    });
    return map;
  }, [profileMissIds, profileQueries]);

  // Working chrome = live Activity work only (Frank 2026-07-24): never fold
  // terminal `no_reply` / `failed` into the header Working list — those stay
  // on Activity / composer surfaces. Also drop identity-unresolved rows so the
  // hover/popover never paints "Unknown Agent".
  const { stoppable, listTasks, runningCount, workingAgentIds, displayNameByAgent } =
    useMemo(() => {
      const stop: ChannelActiveTask[] = [];
      const workingIds = new Set<string>();
      const names = new Map<string, string>();
      let running = 0;
      for (const task of liveTasks) {
        const displayName =
          directoryNamesByAgent.get(task.agent_id) ??
          emitNamesByAgent.get(task.agent_id) ??
          profileNamesByAgent.get(task.agent_id) ??
          null;
        if (!displayName) continue;
        stop.push(task);
        workingIds.add(task.agent_id);
        names.set(task.agent_id, displayName);
        if (task.status === "running") running += 1;
      }
      return {
        stoppable: stop,
        listTasks: stop,
        runningCount: running,
        workingAgentIds: workingIds,
        displayNameByAgent: names,
      };
    }, [
      liveTasks,
      directoryNamesByAgent,
      emitNamesByAgent,
      profileNamesByAgent,
    ]);

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

  // Outer chip is faces (+ working ring motion) only — no N · M / K working
  // text (Frank red-box 2026-07-24). Counts stay available to screen readers.
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
    : t(($) => $.header.view_members_aria);

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
            displayName={displayNameByAgent.get(task.agent_id) ?? task.agent_id}
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
            }}
            className={cn(
              // Separator ring only when idle — working faces use a thin brand
              // breathing ring alone (no enter-pop / thick ring / idle motion).
              "relative inline-flex rounded-full",
              !isWorkingFace && "ring-2 ring-background",
            )}
          >
            <ActorAvatar
              actorType={m.member_type === "agent" ? "agent" : "member"}
              actorId={m.member_id}
              size={FACE_SIZE}
              avatarUrlHint={m.avatar_url}
              // Dense facepile: status-dot punch-outs collide with neighbor rings
              // (Frank 2026-07-24 shot). Working = light brand ring only.
              showStatusDot={false}
              profileLink={false}
            />
            {isWorkingFace ? (
              <span
                aria-hidden
                className="pointer-events-none absolute inset-[-1px] rounded-full border border-brand/50 motion-reduce:animate-none animate-[presence-ring-pulse_2.2s_ease-in-out_infinite]"
              />
            ) : null}
          </span>
        );
      })}
    </span>
  );

  const triggerClass = cn(
    // ≥32px touch; no chip chrome (border/bg/pill) per LRM-587.
    // Faces-only outer chip — no count / working text gap after the stack.
    "inline-flex min-h-8 min-w-8 items-center rounded-md py-0.5 pl-1 pr-1.5 text-foreground outline-none transition-colors",
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
          </button>
        }
      />
      <HoverCardContent align="end" className="w-72 p-3">
        {listBody}
      </HoverCardContent>
    </HoverCard>
  );
}
