"use client";

import type {
  ResearchActiveAssignment,
  ResearchSession,
} from "@multica/core/types/research";
import { useT } from "../../i18n/use-t";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import {
  activeResearchSessions,
  knownResearchAttentionKind,
  selectedResearchSession,
} from "../lib/research-home-selection";
import vistaAsset from "../assets/pixel-sector-vista.png";

const STAGES = ["s1_plan", "s2_sources", "s3_validation", "s4_delivery"] as const;

/**
 * Nameplate anchor points, in percent of the sector view box. The lower band
 * is reserved for the focus plate and the stats strip, so all slots sit in
 * the upper ~60% of the vista.
 */
const SLOTS = [
  { left: 16, top: 20 },
  { left: 82, top: 18 },
  { left: 80, top: 52 },
  { left: 18, top: 55 },
] as const;

const vistaSrc = typeof vistaAsset === "string" ? vistaAsset : vistaAsset.src;

/**
 * LRM-783 home preview on real projection data (critique 2026-08-21), pixel
 * theme 2026-08-22: the vista is a hand-shaded pixel sector painting; the
 * focused run is the gold planet (focus plate), and its actual
 * `active_assignments` are named satellites pinned with nameplates — every
 * plate is a real "agent is working on this run" relationship. Workspace
 * aggregates stay in the honest stats band; no decorative pseudo-topology.
 */
export function ResearchHomeConstellationPreview({ sessions, selectedId }: { sessions: ResearchSession[]; selectedId: string | null }) {
  const { t } = useT("research");
  const active = activeResearchSessions(sessions);
  const focus = selectedResearchSession(sessions, selectedId);
  const progress = focus?.list_progress;
  const agents = new Set(active.flatMap((session) => (session.active_assignments ?? []).filter((item) => item.state === "running").map((item) => item.agent_id))).size;
  const evidence = active.reduce((sum, session) => sum + (session.list_progress?.evidence_count ?? 0), 0);
  const questions = active.reduce((sum, session) => sum + (session.list_progress?.open_question_count ?? 0), 0);
  const attention = active.filter((session) => Boolean(knownResearchAttentionKind(session.list_progress?.attention_kind))).length;
  const stage = focus?.current_stage ?? "s1_plan";
  const currentStage = Math.max(0, STAGES.indexOf(stage as (typeof STAGES)[number]));
  const focusAttention = knownResearchAttentionKind(progress?.attention_kind);

  const assignments = (focus?.active_assignments ?? [])
    .toSorted((a, b) => Number(b.state === "running") - Number(a.state === "running"))
    .slice(0, SLOTS.length);

  const agentName = (assignment: ResearchActiveAssignment) => {
    const member = focus?.fleet_preview?.find((item) => item.agent_id === assignment.agent_id);
    return member?.display_name || member?.name || assignment.role || t(($) => $.home_overview.agent);
  };

  const stats: Array<{ key: string; label: string; value: number }> = [
    { key: "active", label: t(($) => $.home_overview.active), value: active.length },
    { key: "agents", label: t(($) => $.home_overview.agent_short), value: agents },
    { key: "evidence", label: t(($) => $.home_overview.evidence_short), value: evidence },
    attention > 0
      ? { key: "attention", label: t(($) => $.home_overview.attention), value: attention }
      : { key: "questions", label: t(($) => $.home_overview.open_questions_short), value: questions },
  ];

  return (
    <div className="research-home-constellation" data-testid="research-home-constellation" aria-label={t(($) => $.home_overview.constellation_label)}>
      <div className="hidden sm:block">
        <img className="research-home-vista" src={vistaSrc} alt="" aria-hidden />
        {assignments.map((assignment, index) => {
          const slot = SLOTS[index]!;
          const name = agentName(assignment);
          const running = assignment.state === "running";
          const stateLabel = running
            ? t(($) => $.home_overview.assignment_running)
            : t(($) => $.home_overview.waiting_dispatch);
          return (
            <Tooltip key={assignment.task_id || assignment.agent_id}>
              <TooltipTrigger
                render={
                  <div
                    className="research-home-plate"
                    style={{ left: `${slot.left}%`, top: `${slot.top}%` }}
                    data-testid="research-home-constellation-agent"
                    aria-label={`${name} · ${stateLabel}`}
                  />
                }
              >
                <span className="block max-w-[112px] truncate text-xs text-foreground">{name}</span>
                <span className={`research-home-plate-state block truncate ${running ? "research-home-plate-state-on" : ""}`}>
                  {stateLabel}
                </span>
              </TooltipTrigger>
              <TooltipContent side="top">{assignment.task_title || name}</TooltipContent>
            </Tooltip>
          );
        })}
        <div className="research-home-focus-plate">
          <Tooltip>
            <TooltipTrigger render={<span className="block max-w-[300px] truncate text-sm text-foreground" />}>
              {focus?.title || focus?.goal || t(($) => $.home_overview.constellation_empty)}
            </TooltipTrigger>
            <TooltipContent side="top">{focus?.title || focus?.goal}</TooltipContent>
          </Tooltip>
          <span className="text-xs text-muted-foreground">
            <span className="text-success">{t(($) => $.stage_short[stage as keyof typeof $.stage_short] ?? stage)}</span>
            <span aria-hidden> · </span>
            <span className="tabular-nums">{progress ? t(($) => $.home_overview.tasks, { done: progress.task_completed, total: progress.task_total }) : "—"}</span>
          </span>
          {focus && assignments.length === 0 ? (
            <span className="text-xs text-muted-foreground">{t(($) => $.home_overview.assignments_idle)}</span>
          ) : null}
          {focusAttention ? (
            <span className="research-home-attention-tag text-xs">
              {t(($) => $.home_overview.needs_attention)}
            </span>
          ) : null}
        </div>
        <dl className="research-home-constellation-stats" data-testid="research-home-constellation-stats">
          {stats.map((item) => (
            <div key={item.key} className="research-home-stat" data-stat={item.key}>
              <dt className="truncate text-[11px] text-muted-foreground">{item.label}</dt>
              <dd className="text-sm tabular-nums text-foreground">{item.value}</dd>
            </div>
          ))}
        </dl>
      </div>
      <div className="absolute inset-x-4 top-1/2 flex -translate-y-1/2 sm:hidden" aria-label={t(($) => $.home_overview.stage_progress)}>
        {STAGES.map((item, index) => <div key={item} className="relative flex min-w-0 flex-1 flex-col items-center text-center">{index > 0 ? <span className={`absolute right-1/2 top-3 h-px w-full ${index <= currentStage ? "bg-success/60" : "bg-border"}`} aria-hidden /> : null}<span className={`relative z-[1] grid size-7 place-items-center border-2 bg-card text-xs ${index === currentStage ? "border-brand text-brand" : index < currentStage ? "border-success text-success" : "border-border text-muted-foreground"}`}>{index + 1}</span><span className="mt-2 text-xs text-muted-foreground">{t(($) => $.stage_short[item])}</span></div>)}
      </div>
    </div>
  );
}
