"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  CheckCircle2,
  ChevronRight,
  CircleSlash,
  Loader2,
  Medal,
  Pencil,
  RotateCcw,
  Sparkles,
  Trash2,
  XCircle,
  type LucideIcon,
} from "lucide-react";
import type { Agent, AgentRuntime, AgentTask, AgentFleetRank } from "@multica/core/types";
import { FleetRankBadge } from "@multica/ui/components/fleet/fleet-class-badge";
import { agentTasksOptions, type AgentPresence } from "@multica/core/agents";
import { resolveActorDisplayName, resolveActorHandle } from "@multica/core/identity";
import { deriveRuntimeHealth } from "@multica/core/runtimes";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { useT, useTimeAgo } from "../../i18n";
import { AgentRestartModal } from "./agent-restart-modal";
import { AgentOpenDmButton } from "./agent-open-dm-button";
import { AgentHonorLevelIcon } from "./agent-honor-level-icon";
import { useAgentFleetClassName } from "../hooks/use-agent-fleet-class-name";

export interface AgentMetric {
  /** Cumulative runs in the last 30d. */
  runCount: number;
  /** 0–100, or null when there's no terminal-task data to derive it from. */
  successRate: number | null;
  /** USD over the last 30d, or null when usage isn't priced/available. */
  cost: number | null;
}

function StatCard({
  label,
  value,
  sub,
}: {
  label: string;
  value: string;
  sub: string;
}) {
  return (
    <div className="flex flex-col rounded-xl border border-border/60 bg-card px-4 py-3">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="mt-1.5 text-2xl font-semibold tabular-nums leading-none">{value}</span>
      <span className="mt-1.5 text-xs text-muted-foreground">{sub}</span>
    </div>
  );
}

function SectionCard({
  title,
  action,
  children,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col rounded-xl border border-border/60 bg-card p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">{title}</h3>
        {action}
      </div>
      {children}
    </div>
  );
}

function FleetHonorCard({
  fleet,
  honorLevel,
  isArchived,
  classLabel,
  onHonor,
}: {
  fleet: AgentFleetRank;
  honorLevel?: number;
  isArchived: boolean;
  classLabel: string;
  onHonor: () => void;
}) {
  const { t } = useT("agents");
  const pillars = (
    [
      ["delivery", fleet.pillars.delivery],
      ["evolution", fleet.pillars.evolution],
      ["growth", fleet.pillars.growth],
      ["efficiency", fleet.pillars.efficiency],
    ] as const
  ).map(([key, value]) => ({
    key,
    percent: Math.round(Math.max(0, Math.min(1, value)) * 100),
  }));
  const frozen = fleet.frozen || isArchived;

  return (
    <section
      className={cn(
        "group relative isolate w-full overflow-hidden rounded-2xl border border-primary/20",
        "bg-gradient-to-br from-primary/[0.09] via-card to-chart-2/[0.08] p-4 text-left shadow-sm",
        "transition-[transform,border-color,box-shadow] duration-300 hover:-translate-y-0.5 hover:border-primary/40 hover:shadow-lg",
        "motion-reduce:transform-none motion-reduce:transition-none",
        frozen && "opacity-80",
      )}
    >
      <button
        type="button"
        data-testid="agent-fleet-honor-card"
        onClick={onHonor}
        className="absolute inset-0 z-20 cursor-pointer rounded-2xl bg-transparent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      >
        <span className="sr-only">
          {t(($) => $.fleet.title)} · {t(($) => $.tabs.honor)}
        </span>
      </button>
      <span
        aria-hidden="true"
        className="pointer-events-none absolute -right-16 -top-20 -z-10 size-56 rounded-full bg-primary/15 blur-3xl transition-transform duration-500 group-hover:scale-110 motion-reduce:transition-none"
      />
      <span
        aria-hidden="true"
        className="pointer-events-none absolute -bottom-24 left-1/3 -z-10 size-52 rounded-full bg-chart-2/10 blur-3xl"
      />
      <div className="relative flex flex-col gap-5">
        <div className="flex items-start justify-between gap-4">
          <div className="flex min-w-0 items-center gap-3">
            {honorLevel !== undefined ? (
              <AgentHonorLevelIcon
                level={honorLevel}
                title={t(($) => $.honor_agent.level_value, { level: honorLevel })}
                className="size-20 drop-shadow-xl transition-transform duration-300 group-hover:scale-105 motion-reduce:transition-none"
              />
            ) : null}
            <span className="min-w-0">
              <span className="block text-[11px] font-medium tracking-[0.14em] text-muted-foreground">
                {t(($) => $.fleet.title)}
              </span>
              <span className="mt-1.5 flex flex-wrap items-center gap-2">
                <FleetRankBadge
                  classLabel={classLabel}
                  fleetRank={fleet.fleet_rank}
                  frozen={frozen}
                />
                {!fleet.sample_sufficient ? (
                  <span className="text-xs text-muted-foreground">
                    {t(($) => $.fleet.warming_up, {
                      current: fleet.sample_tasks,
                      required: fleet.min_sample_tasks,
                    })}
                  </span>
                ) : null}
              </span>
            </span>
          </div>

          <span className="inline-flex shrink-0 items-center gap-1 rounded-full border border-primary/20 bg-background/70 px-2.5 py-1 text-xs font-medium text-primary backdrop-blur-sm">
            {t(($) => $.tabs.honor)}
            <ChevronRight
              aria-hidden="true"
              className="size-3.5 transition-transform group-hover:translate-x-0.5 motion-reduce:transition-none"
            />
          </span>
        </div>

        <div className="grid gap-4 md:grid-cols-[minmax(150px,0.35fr)_minmax(0,1fr)] md:items-end">
          <span className="rounded-xl border border-border/50 bg-background/55 px-3.5 py-3 backdrop-blur-sm">
            <span className="block text-xs text-muted-foreground">
              {t(($) => $.fleet.score_label)}
            </span>
            <span className="mt-1 flex items-baseline gap-2">
              <span className="text-4xl font-semibold tabular-nums tracking-tight text-foreground">
                {Math.round(fleet.fleet_score)}
              </span>
            </span>
            <span className="mt-1 block text-xs text-muted-foreground">
              {frozen
                ? t(($) => $.fleet.frozen_hint)
                : t(($) => $.fleet.rank_of, {
                    rank: fleet.fleet_rank,
                    size: fleet.fleet_size,
                  })}
            </span>
          </span>

          <span className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {pillars.map(({ key, percent }) => (
              <span
                key={key}
                className="rounded-xl border border-border/50 bg-background/55 px-3 py-2.5 backdrop-blur-sm"
              >
                <span className="flex items-center justify-between gap-2">
                  <span className="text-xs text-muted-foreground">
                    {t(($) => $.fleet.pillars[key])}
                  </span>
                  <span className="text-sm font-semibold tabular-nums text-foreground">
                    {percent}
                  </span>
                </span>
                <span
                  aria-hidden="true"
                  className="mt-2 block h-1.5 overflow-hidden rounded-full bg-muted"
                >
                  <span
                    className="block h-full rounded-full bg-gradient-to-r from-primary to-chart-2 transition-[width] duration-500 motion-reduce:transition-none"
                    style={{ width: `${percent}%` }}
                  />
                </span>
              </span>
            ))}
          </span>
        </div>
      </div>
    </section>
  );
}

// Map a task's terminal/active status to an icon + color for the log row.
function taskVisual(status: AgentTask["status"]): { Icon: LucideIcon; cls: string } {
  switch (status) {
    case "completed":
      return { Icon: CheckCircle2, cls: "text-success" };
    case "failed":
      return { Icon: XCircle, cls: "text-destructive" };
    case "cancelled":
      return { Icon: CircleSlash, cls: "text-muted-foreground" };
    case "running":
    case "dispatched":
    case "queued":
    default:
      return { Icon: Loader2, cls: "text-brand" };
  }
}

function ExecLogRow({ task }: { task: AgentTask }) {
  const { t } = useT("agents");
  const timeAgo = useTimeAgo();
  const { Icon, cls } = taskVisual(task.status);
  const isRunning = task.status === "running" || task.status === "dispatched";

  const title =
    task.trigger_summary?.trim() ||
    (task.issue_id ? `#${task.issue_id.slice(0, 8)}` : t(($) => $.dashboard.task_fallback));

  // Duration: terminal tasks use started→completed; running tasks use
  // started→now. Both best-effort — missing timestamps just hide the hint.
  const sub = useMemo(() => {
    const start = task.started_at ? Date.parse(task.started_at) : NaN;
    if (isRunning && !Number.isNaN(start)) {
      const min = Math.max(1, Math.round((Date.now() - start) / 60000));
      return t(($) => $.dashboard.running_for, { min });
    }
    const end = task.completed_at ? Date.parse(task.completed_at) : NaN;
    const when = task.completed_at ?? task.created_at;
    if (!Number.isNaN(start) && !Number.isNaN(end) && end >= start) {
      const min = Math.max(1, Math.round((end - start) / 60000));
      return `${timeAgo(when)} · ${t(($) => $.dashboard.duration_min, { min })}`;
    }
    return timeAgo(when);
  }, [task, isRunning, t, timeAgo]);

  return (
    <li className="flex items-start gap-2.5 py-2">
      <Icon className={cn("mt-0.5 size-4 shrink-0", cls, isRunning && "animate-spin")} />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm text-foreground">{title}</p>
        <p className="mt-0.5 text-xs text-muted-foreground">{sub}</p>
      </div>
    </li>
  );
}

export function AgentDetailOverview({
  agent,
  runtime,
  metric,
  fleet,
  presence,
  canManage,
  canLifecycle,
  onHonor,
  onEdit,
  onDelete,
}: {
  agent: Agent;
  runtime: AgentRuntime | null;
  metric: AgentMetric;
  fleet?: AgentFleetRank;
  presence: AgentPresence | "loading";
  canManage: boolean;
  canLifecycle: boolean;
  onHonor: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("agents");
  const fleetClassName = useAgentFleetClassName();
  const wsId = useWorkspaceId();
  const { data: tasks = [] } = useQuery(agentTasksOptions(wsId, agent.id));
  const recentTasks = useMemo(() => tasks.slice(0, 6), [tasks]);
  const [restartOpen, setRestartOpen] = useState(false);

  const isArchived = !!agent.archived_at;
  const costText = metric.cost === null ? "—" : `$${metric.cost.toFixed(2)}`;
  const successText = metric.successRate === null ? "—" : `${Math.round(metric.successRate)}%`;
  // Agents list detail previously had Honor/Edit/Delete only — no Restart.
  // Parker #26: canManage + online → show Restart; no force → disable + copy.
  const forceRestartSupported =
    runtime?.provider_capabilities?.force_restart ?? false;
  const isRuntimeOnline =
    !!runtime &&
    deriveRuntimeHealth(
      {
        status: runtime.status,
        last_seen_at: runtime.last_seen_at ?? null,
      },
      Date.now(),
    ) === "online";
  const showRestart = canManage && !isArchived && isRuntimeOnline;
  const restartBlocked = !forceRestartSupported;
  const agentHandle = resolveActorHandle(agent) ?? agent.name;
  const agentName = resolveActorDisplayName(agent, agent.id);

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      {/* Header */}
      <div className="flex items-start justify-between gap-3 border-b px-6 py-4">
        <div className="flex min-w-0 items-center gap-3">
          <ActorAvatar
            actorType="agent"
            actorId={agent.id}
            size={40}
            className={cn("shrink-0", isArchived && "opacity-50 grayscale")}
            showStatusDot={!isArchived}
            agentPresence={presence}
          />
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <ActorIdentityRow
                identity={agent}
                agentHonorLevel={agent.honor_level}
                primaryClassName={cn(
                  "truncate text-base font-semibold",
                  isArchived && "text-muted-foreground",
                )}
                className="min-w-0 shrink"
              />
              {isArchived ? (
                <span className="text-xs text-muted-foreground">
                  {t(($) => $.row.archived)}
                </span>
              ) : null}
            </div>
            <p className="mt-0.5 truncate text-xs text-muted-foreground">
              {agent.description?.trim() || (runtime?.name ?? t(($) => $.dashboard.no_description))}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
          {!isArchived ? (
            <AgentOpenDmButton agentId={agent.id} variant="labeled" />
          ) : null}
          <Button variant="outline" size="sm" onClick={onHonor}>
            <Medal className="size-3.5" />
            {t(($) => $.tabs.honor)}
          </Button>
          <Button variant="outline" size="sm" onClick={onEdit}>
            <Pencil className="size-3.5" />
            {t(($) => $.dashboard.edit_config)}
          </Button>
          {showRestart ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              data-testid="agent-detail-action-restart"
              disabled={restartBlocked}
              title={
                restartBlocked
                  ? t(($) => $.restart_modal.disabled_reason.no_force_capability)
                  : undefined
              }
              onClick={() => {
                if (!restartBlocked) setRestartOpen(true);
              }}
            >
              <RotateCcw className="size-3.5" />
              {t(($) => $.restart_modal.trigger)}
            </Button>
          ) : null}
          {canLifecycle && (
            <Button variant="outline" size="sm" onClick={onDelete} className="text-destructive hover:text-destructive">
              <Trash2 className="size-3.5" />
              {t(($) => $.dashboard.delete)}
            </Button>
          )}
        </div>
      </div>

      <div className="flex flex-col gap-4 p-6">
        {/* Metric cards */}
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <StatCard label={t(($) => $.dashboard.metric_tasks)} value={String(metric.runCount)} sub={t(($) => $.dashboard.last_30d)} />
          <StatCard label={t(($) => $.dashboard.metric_success)} value={successText} sub={t(($) => $.dashboard.last_30d)} />
          <StatCard label={t(($) => $.dashboard.metric_cost)} value={costText} sub={t(($) => $.dashboard.last_30d)} />
          <StatCard
            label={t(($) => $.fleet.score_label)}
            value={fleet ? Math.round(fleet.fleet_score).toString() : "—"}
            sub={
              fleet
                ? fleet.frozen || isArchived
                  ? t(($) => $.fleet.frozen_hint)
                  : t(($) => $.fleet.rank_of, { rank: fleet.fleet_rank, size: fleet.fleet_size })
                : t(($) => $.dashboard.last_30d)
            }
          />
        </div>

        {fleet ? (
          <FleetHonorCard
            fleet={fleet}
            honorLevel={agent.honor_level}
            isArchived={isArchived}
            classLabel={fleetClassName(fleet.class_id, fleet.class_label)}
            onHonor={onHonor}
          />
        ) : null}

        {/* Role & capabilities + Prompt strategy */}
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <SectionCard
            title={t(($) => $.dashboard.role_capabilities)}
            action={
              <button type="button" onClick={onEdit} className="text-xs text-muted-foreground transition-colors hover:text-foreground">
                {t(($) => $.dashboard.edit)}
              </button>
            }
          >
            <p className="text-sm leading-relaxed text-muted-foreground">
              {agent.description?.trim() || t(($) => $.dashboard.no_description)}
            </p>
            {agent.skills.length > 0 ? (
              <div className="mt-3 flex flex-wrap gap-1.5">
                {agent.skills.map((s) => (
                  <span key={s.id} className="inline-flex items-center rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                    {s.name}
                  </span>
                ))}
              </div>
            ) : (
              <p className="mt-3 text-xs text-muted-foreground/70">{t(($) => $.dashboard.no_skills)}</p>
            )}
          </SectionCard>

          <SectionCard title={t(($) => $.dashboard.prompt_strategy)}>
            {agent.instructions?.trim() ? (
              <p className="max-h-48 overflow-y-auto whitespace-pre-wrap text-sm leading-relaxed text-muted-foreground">
                {agent.instructions}
              </p>
            ) : (
              <p className="text-xs text-muted-foreground/70">{t(($) => $.dashboard.no_prompt)}</p>
            )}
          </SectionCard>
        </div>

        {/* Execution log + Self-evolution */}
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <SectionCard
            title={t(($) => $.dashboard.exec_log)}
            action={
              <button type="button" onClick={onEdit} className="text-xs text-muted-foreground transition-colors hover:text-foreground">
                {t(($) => $.dashboard.view_all)}
              </button>
            }
          >
            {recentTasks.length > 0 ? (
              <ul className="-my-2 divide-y divide-border/50">
                {recentTasks.map((task) => (
                  <ExecLogRow key={task.id} task={task} />
                ))}
              </ul>
            ) : (
              <p className="text-xs text-muted-foreground/70">{t(($) => $.dashboard.no_logs)}</p>
            )}
          </SectionCard>

          <SectionCard title={t(($) => $.dashboard.evolution)}>
            <div className="flex flex-col items-center justify-center gap-2 py-8 text-center">
              <Sparkles className="size-6 text-muted-foreground/40" />
              <p className="max-w-xs text-xs text-muted-foreground">{t(($) => $.dashboard.evolution_placeholder)}</p>
            </div>
          </SectionCard>
        </div>
      </div>

      {showRestart ? (
        <AgentRestartModal
          agentId={agent.id}
          agentHandle={agentHandle}
          agentName={agentName}
          open={restartOpen}
          onOpenChange={setRestartOpen}
        />
      ) : null}
    </div>
  );
}
