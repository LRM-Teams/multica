"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  CheckCircle2,
  CircleSlash,
  Loader2,
  Medal,
  Pencil,
  Sparkles,
  Trash2,
  XCircle,
  type LucideIcon,
} from "lucide-react";
import type { Agent, AgentRuntime, AgentTask, AgentFleetRank } from "@multica/core/types";
import { FleetRankBadge } from "@multica/ui/components/fleet/fleet-class-badge";
import { agentTasksOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { useT, useTimeAgo } from "../../i18n";

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
    case "waiting_local_directory":
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
  canManage,
  onHonor,
  onEdit,
  onDelete,
}: {
  agent: Agent;
  runtime: AgentRuntime | null;
  metric: AgentMetric;
  fleet?: AgentFleetRank;
  canManage: boolean;
  onHonor: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const { data: tasks = [] } = useQuery(agentTasksOptions(wsId, agent.id));
  const recentTasks = useMemo(() => tasks.slice(0, 6), [tasks]);

  const isArchived = !!agent.archived_at;
  const costText = metric.cost === null ? "—" : `$${metric.cost.toFixed(2)}`;
  const successText = metric.successRate === null ? "—" : `${Math.round(metric.successRate)}%`;

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
          />
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <ActorIdentityRow
                identity={agent}
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
          <Button variant="outline" size="sm" onClick={onHonor}>
            <Medal className="size-3.5" />
            {t(($) => $.tabs.honor)}
          </Button>
          <Button variant="outline" size="sm" onClick={onEdit}>
            <Pencil className="size-3.5" />
            {t(($) => $.dashboard.edit_config)}
          </Button>
          {canManage && (
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
          <SectionCard title={t(($) => $.fleet.title)}>
            <div className="flex flex-wrap items-center gap-3">
              <FleetRankBadge
                classId={fleet.class_id}
                classLabel={fleet.class_label}
                fleetRank={fleet.fleet_rank}
                frozen={fleet.frozen || isArchived}
              />
              {!fleet.sample_sufficient ? (
                <span className="text-xs text-muted-foreground">
                  {t(($) => $.fleet.warming_up, {
                    current: fleet.sample_tasks,
                    required: 5,
                  })}
                </span>
              ) : null}
            </div>
            <div className="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-4">
              {(
                [
                  ["delivery", fleet.pillars.delivery],
                  ["evolution", fleet.pillars.evolution],
                  ["growth", fleet.pillars.growth],
                  ["efficiency", fleet.pillars.efficiency],
                ] as const
              ).map(([key, value]) => (
                <div key={key} className="rounded-lg border border-border/60 px-3 py-2">
                  <div className="text-xs text-muted-foreground">{t(($) => $.fleet.pillars[key])}</div>
                  <div className="text-sm font-semibold tabular-nums">{Math.round(value * 100)}</div>
                </div>
              ))}
            </div>
          </SectionCard>
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
    </div>
  );
}
