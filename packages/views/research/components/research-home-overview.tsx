"use client";

import type { ResearchSession } from "@multica/core/types/research";
import { cn } from "@multica/ui/lib/utils";
import { AlertTriangle, Check, FileSearch, GitBranch, Radio } from "lucide-react";
import { AgentAvatarStack } from "../../agents/components/agent-avatar-stack";
import { Time } from "../../i18n/time";
import { useT } from "../../i18n/use-t";
import { AppLink } from "../../navigation/app-link";

const STAGES = ["s1_plan", "s2_sources", "s3_validation", "s4_delivery"] as const;

function stageIndex(stage: string): number {
  const index = STAGES.indexOf(stage as (typeof STAGES)[number]);
  return index < 0 ? 0 : index;
}

function attentionRank(session: ResearchSession): number {
  if (session.list_progress?.awaiting_user_action) return 0;
  if ((session.list_progress?.task_blocked ?? 0) > 0) return 1;
  if (session.status === "running") return 2;
  if (session.status === "paused") return 3;
  return 4;
}

export function ResearchHomeOverview({
  sessions,
  hrefFor,
  onNavigate,
}: {
  sessions: ResearchSession[];
  hrefFor: (id: string) => string;
  onNavigate?: (id: string) => void;
}) {
  const { t } = useT("research");
  const active = sessions
    .filter((session) => !["completed", "archived", "cancelled"].includes(session.status))
    .sort((a, b) => {
      const rank = attentionRank(a) - attentionRank(b);
      if (rank !== 0) return rank;
      return Date.parse(b.list_progress?.last_progress_at ?? b.updated_at) - Date.parse(a.list_progress?.last_progress_at ?? a.updated_at);
    })
    .slice(0, 6);
  if (active.length === 0) return null;

  const evidence = sessions.reduce((sum, session) => sum + (session.list_progress?.evidence_count ?? 0), 0);
  const workingAgents = new Set(
    active.flatMap((session) => (session.fleet_preview ?? []).map((member) => member.agent_id)),
  ).size;
  const needsAttention = sessions.filter((session) => session.list_progress?.awaiting_user_action || (session.list_progress?.task_blocked ?? 0) > 0).length;
  const summary = [
    [t(($) => $.home_overview.total), sessions.length],
    [t(($) => $.home_overview.active), active.length],
    [t(($) => $.home_overview.agents), workingAgents],
    [t(($) => $.home_overview.evidence), evidence],
    [t(($) => $.home_overview.attention), needsAttention],
  ];

  return (
    <section className="relative z-[1] space-y-4" data-testid="research-home-overview">
      <dl className="grid grid-cols-2 overflow-hidden rounded-xl border border-border/80 bg-card/75 sm:grid-cols-5">
        {summary.map(([label, value], index) => (
          <div key={String(label)} className={cn("px-3 py-3", index > 0 && "border-l border-border/70", index === 4 && "col-span-2 border-t sm:col-span-1 sm:border-t-0")}>
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className={cn("mt-1 text-base font-medium tabular-nums", index === 4 && Number(value) > 0 ? "text-warning" : "text-foreground")}>{value}</dd>
          </div>
        ))}
      </dl>

      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-medium text-foreground">{t(($) => $.home_overview.heading)}</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">{t(($) => $.home_overview.subtitle)}</p>
        </div>
        <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
          <Radio className="size-3.5 text-success" aria-hidden />
          {t(($) => $.home_overview.live)}
        </span>
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2 xl:grid-cols-3">
        {active.map((session) => {
          const progress = session.list_progress;
          const current = stageIndex(session.current_stage);
          const fleetIds = (session.fleet_preview ?? []).map((member) => member.agent_id);
          const attention = progress?.awaiting_user_action || (progress?.task_blocked ?? 0) > 0;
          return (
            <article key={session.id} className={cn("group relative overflow-hidden rounded-xl border bg-card/88 p-4 transition-colors hover:bg-card", attention ? "border-warning/40" : "border-border/85")}>
              <AppLink href={hrefFor(session.id)} onClick={() => onNavigate?.(session.id)} className="absolute inset-0 rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring" aria-label={session.title || session.goal} />
              <div className="relative pointer-events-none">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className={cn("size-2 rounded-full", attention ? "bg-warning" : session.status === "running" ? "bg-brand motion-safe:animate-pulse" : "bg-muted-foreground")} aria-hidden />
                      <h3 className="line-clamp-2 text-sm font-medium text-foreground">{session.title || session.goal}</h3>
                    </div>
                    <p className="mt-1 pl-4 text-xs text-muted-foreground">{t(($) => $.stage[session.current_stage as keyof typeof $.stage] ?? session.current_stage)}</p>
                  </div>
                  <AgentAvatarStack agentIds={fleetIds} size={22} max={3} />
                </div>

                <div className="mt-5 flex items-center" aria-label={t(($) => $.home_overview.stage_progress)}>
                  {STAGES.map((stage, index) => (
                    <div key={stage} className="flex min-w-0 flex-1 items-center">
                      <span className={cn("flex size-7 shrink-0 items-center justify-center rounded-full border text-xs font-medium", index < current || session.status === "completed" ? "border-success/55 bg-success/12 text-success" : index === current ? attention ? "border-warning/60 bg-warning/12 text-warning" : "border-brand/60 bg-brand/12 text-brand" : "border-border text-muted-foreground")}>
                        {index < current || session.status === "completed" ? <Check className="size-3.5" aria-hidden /> : index + 1}
                      </span>
                      {index < STAGES.length - 1 ? <span className={cn("h-px min-w-2 flex-1", index < current ? "bg-success/55" : "bg-border")} aria-hidden /> : null}
                    </div>
                  ))}
                </div>

                <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs text-muted-foreground">
                  {progress ? (
                    <>
                      <span className="inline-flex items-center gap-1"><GitBranch className="size-3.5" aria-hidden />{t(($) => $.home_overview.tasks, { done: progress.task_completed, total: progress.task_total })}</span>
                      <span className="inline-flex items-center gap-1"><FileSearch className="size-3.5" aria-hidden />{t(($) => $.home_overview.evidence_count, { count: progress.evidence_count })}</span>
                      {progress.task_blocked > 0 ? <span className="inline-flex items-center gap-1 text-warning"><AlertTriangle className="size-3.5" aria-hidden />{t(($) => $.home_overview.blocked, { count: progress.task_blocked })}</span> : null}
                    </>
                  ) : <span>{t(($) => $.home_overview.progress_unavailable)}</span>}
                  <Time kind="list" value={progress?.last_progress_at ?? session.updated_at} className="ml-auto tabular-nums" />
                </div>
              </div>
            </article>
          );
        })}
      </div>
    </section>
  );
}
