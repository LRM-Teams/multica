"use client";

import { useMemo, useState } from "react";
import type { ResearchSession } from "@multica/core/types/research";
import { cn } from "@multica/ui/lib/utils";
import { AlertTriangle, ArrowRight, Check, FileCheck2, GitBranch, Radio, Users } from "lucide-react";
import { ActorAvatar } from "../../common/actor-avatar";
import { Time } from "../../i18n/time";
import { useT } from "../../i18n/use-t";
import { AppLink } from "../../navigation/app-link";

const STAGES = ["s1_plan", "s2_sources", "s3_validation", "s4_delivery"] as const;

function stageIndex(stage: string): number {
  const index = STAGES.indexOf(stage as (typeof STAGES)[number]);
  return index < 0 ? 0 : index;
}

function rank(session: ResearchSession): number {
  const kind = session.list_progress?.attention_kind;
  if (kind === "user_confirmation") return 0;
  if (kind === "blocked_tasks") return 1;
  if (kind === "recoverable_failure") return 2;
  if (session.status === "running") return 3;
  if (session.status === "paused") return 4;
  return 5;
}

function isActive(session: ResearchSession): boolean {
  if (["completed", "archived", "cancelled"].includes(session.status)) return false;
  return session.status !== "failed" || session.list_progress?.recoverable === true;
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
  const active = useMemo(
    () => sessions.filter(isActive).sort((a, b) => rank(a) - rank(b) || Date.parse(b.list_progress?.last_progress_at ?? b.updated_at) - Date.parse(a.list_progress?.last_progress_at ?? a.updated_at)),
    [sessions],
  );
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const selected = active.find((session) => session.id === selectedId) ?? active[0] ?? null;

  if (!selected) return null;

  const workingAgents = new Set(active.flatMap((session) => (session.active_assignments ?? []).map((assignment) => assignment.agent_id))).size;
  const needsAttention = active.filter((session) => Boolean(session.list_progress?.attention_kind)).length;
  const todayEvidence = sessions.reduce((sum, session) => sum + (session.list_progress?.today_evidence_count ?? 0), 0);
  const summary = [
    [t(($) => $.home_overview.active), active.length, "text-brand"],
    [t(($) => $.home_overview.attention), needsAttention, needsAttention > 0 ? "text-warning" : "text-foreground"],
    [t(($) => $.home_overview.agents), workingAgents, "text-foreground"],
    [t(($) => $.home_overview.today_evidence), todayEvidence, "text-success"],
  ] as const;

  return (
    <section className="relative z-[1] space-y-3" data-testid="research-home-overview">
      <dl className="grid grid-cols-2 overflow-hidden rounded-xl border border-border/80 bg-card/72 sm:grid-cols-4">
        {summary.map(([label, value, tone], index) => (
          <div key={label} className={cn("px-4 py-2.5", index > 0 && "border-l border-border/70", index > 1 && "border-t sm:border-t-0")}>
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className={cn("mt-0.5 text-base font-medium tabular-nums", tone)}>{value}</dd>
          </div>
        ))}
      </dl>

      <div className={cn("grid gap-3", active.length > 1 && "xl:grid-cols-[minmax(0,3fr)_minmax(320px,2fr)]")}>
        <FocusSession session={selected} href={hrefFor(selected.id)} onNavigate={() => onNavigate?.(selected.id)} />
        {active.length > 1 ? (
          <aside className="overflow-hidden rounded-xl border border-border/85 bg-card/78" aria-label={t(($) => $.home_overview.queue_heading)}>
            <div className="flex items-center justify-between border-b border-border/70 px-4 py-3">
              <div>
                <h2 className="text-sm font-medium text-foreground">{t(($) => $.home_overview.queue_heading)}</h2>
                <p className="mt-0.5 text-xs text-muted-foreground">{t(($) => $.home_overview.queue_subtitle)}</p>
              </div>
              <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"><Radio className="size-3.5 text-success" aria-hidden />{t(($) => $.home_overview.live)}</span>
            </div>
            <div className="max-h-[390px] overflow-y-auto p-1.5">
              {active.map((session) => <QueueRow key={session.id} session={session} selected={session.id === selected.id} onSelect={() => setSelectedId(session.id)} />)}
            </div>
          </aside>
        ) : null}
      </div>
    </section>
  );
}

function FocusSession({ session, href, onNavigate }: { session: ResearchSession; href: string; onNavigate?: () => void }) {
  const { t } = useT("research");
  const progress = session.list_progress;
  const current = stageIndex(session.current_stage);
  const outcomes = session.latest_outcomes ?? [];
  const assignments = session.active_assignments ?? [];
  const attention = progress?.attention_kind;

  return (
    <article className={cn("relative overflow-hidden rounded-xl border bg-card/88 p-4 md:p-5", attention ? "border-warning/40" : "border-brand/25")} data-testid="research-focus-session">
      <div className="pointer-events-none absolute right-[-60px] top-[-80px] size-64 rounded-full bg-brand/9 blur-3xl" aria-hidden />
      <div className="relative">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span className={cn("size-2 rounded-full", attention ? "bg-warning" : "bg-brand motion-safe:animate-pulse")} aria-hidden />
              <span>{attention ? t(($) => $.home_overview.needs_attention) : t(($) => $.home_overview.focus_heading)}</span>
              <span aria-hidden>·</span>
              <Time kind="list" value={progress?.last_progress_at ?? session.updated_at} />
            </div>
            <h2 className="mt-2 line-clamp-2 text-base font-medium text-foreground">{session.title || session.goal}</h2>
            <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.stage[session.current_stage as keyof typeof $.stage] ?? session.current_stage)}</p>
          </div>
          <AppLink href={href} onClick={onNavigate} className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-brand px-3.5 text-sm font-medium text-brand-foreground outline-none transition-colors hover:bg-brand/80 focus-visible:ring-2 focus-visible:ring-ring">
            {attention === "user_confirmation" ? t(($) => $.home_overview.handle_confirmation) : t(($) => $.home_overview.enter)}
            <ArrowRight className="size-3.5" aria-hidden />
          </AppLink>
        </div>

        {attention ? <div className="mt-3 flex items-center gap-2 rounded-lg bg-warning/9 px-3 py-2 text-xs text-warning"><AlertTriangle className="size-3.5 shrink-0" aria-hidden />{t(($) => attention === "user_confirmation" ? $.home_overview.confirmation_reason : attention === "recoverable_failure" ? $.home_overview.recoverable_reason : $.home_overview.blocked_reason, { count: progress?.task_blocked ?? 0 })}</div> : null}

        <div className="mt-5 grid grid-cols-4 gap-0" aria-label={t(($) => $.home_overview.stage_progress)}>
          {STAGES.map((stage, index) => (
            <div key={stage} className="relative flex flex-col items-center text-center">
              {index > 0 ? <span className={cn("absolute right-1/2 top-3 h-px w-full", index <= current ? "bg-success/55" : "bg-border")} aria-hidden /> : null}
              <span className={cn("relative z-[1] flex size-7 items-center justify-center rounded-full border bg-card text-xs font-medium", index < current || session.status === "completed" ? "border-success/60 text-success" : index === current ? attention ? "border-warning text-warning" : "border-brand text-brand" : "border-border text-muted-foreground")}>
                {index < current || session.status === "completed" ? <Check className="size-3.5" aria-hidden /> : index + 1}
              </span>
              <span className={cn("mt-1.5 text-xs", index === current ? "font-medium text-foreground" : "text-muted-foreground")}>{t(($) => $.stage_short[stage])}</span>
            </div>
          ))}
        </div>

        <div className="mt-4 flex flex-wrap gap-x-4 gap-y-2 border-y border-border/65 py-3 text-xs text-muted-foreground">
          <span className="inline-flex items-center gap-1.5"><GitBranch className="size-3.5" aria-hidden />{progress ? t(($) => $.home_overview.tasks, { done: progress.task_completed, total: progress.task_total }) : t(($) => $.home_overview.progress_unavailable)}</span>
          <span className="inline-flex items-center gap-1.5"><FileCheck2 className="size-3.5" aria-hidden />{t(($) => $.home_overview.evidence_count, { count: progress?.evidence_count ?? 0 })}</span>
          <span className="inline-flex items-center gap-1.5"><Users className="size-3.5" aria-hidden />{t(($) => $.home_overview.open_questions, { count: progress?.open_question_count ?? 0 })}</span>
        </div>

        <div className="mt-4 grid gap-4 md:grid-cols-2">
          <section>
            <h3 className="text-xs font-medium text-foreground">{t(($) => $.home_overview.assignments)}</h3>
            <div className="mt-2 space-y-2">
              {assignments.length > 0 ? assignments.map((assignment) => (
                <div key={assignment.task_id} className="flex items-center gap-2">
                  <ActorAvatar actorType="agent" actorId={assignment.agent_id} size={24} profileLink={false} />
                  <div className="min-w-0"><p className="truncate text-xs font-medium text-foreground">{assignment.task_title}</p><p className="text-xs text-muted-foreground">{assignment.role || assignment.state}</p></div>
                </div>
              )) : <p className="text-xs leading-relaxed text-muted-foreground">{t(($) => $.home_overview.assignments_idle)}</p>}
            </div>
          </section>
          <section>
            <h3 className="text-xs font-medium text-foreground">{t(($) => $.home_overview.latest_outcomes)}</h3>
            <div className="mt-2 space-y-2">
              {outcomes.length > 0 ? outcomes.map((outcome) => <div key={outcome.id} className="flex gap-2"><span className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", outcome.kind === "claim" ? "bg-success" : "bg-brand")} aria-hidden /><div className="min-w-0"><p className="line-clamp-1 text-xs font-medium text-foreground">{outcome.title}</p>{outcome.summary ? <p className="mt-0.5 line-clamp-1 text-xs text-muted-foreground">{outcome.summary}</p> : null}</div></div>) : <p className="text-xs leading-relaxed text-muted-foreground">{t(($) => $.home_overview.outcomes_empty)}</p>}
            </div>
          </section>
        </div>
      </div>
    </article>
  );
}

function QueueRow({ session, selected, onSelect }: { session: ResearchSession; selected: boolean; onSelect: () => void }) {
  const { t } = useT("research");
  const progress = session.list_progress;
  return <button type="button" onClick={onSelect} aria-pressed={selected} className={cn("flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left outline-none transition-colors hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring", selected && "bg-muted text-foreground hover:bg-muted")}>
    <span className={cn("size-2 shrink-0 rounded-full", progress?.attention_kind ? "bg-warning" : session.status === "running" ? "bg-brand" : "bg-muted-foreground")} aria-hidden />
    <span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium text-foreground">{session.title || session.goal}</span><span className="mt-0.5 flex items-center gap-2 text-xs text-muted-foreground"><span>{t(($) => $.stage_short[session.current_stage as keyof typeof $.stage_short] ?? session.current_stage)}</span>{progress ? <><span aria-hidden>·</span><span className="tabular-nums">{progress.task_completed}/{progress.task_total} task</span><span aria-hidden>·</span><span>{progress.evidence_count} {t(($) => $.home_overview.evidence_short)}</span></> : null}</span></span>
    <span className="shrink-0 text-xs tabular-nums text-muted-foreground"><Time kind="list" value={progress?.last_progress_at ?? session.updated_at} /></span>
  </button>;
}
