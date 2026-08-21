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

const STAGES = ["s1_plan", "s2_sources", "s3_validation", "s4_delivery"] as const;

/**
 * Satellite anchor points, in percent of the preview box. Edges are drawn in
 * the same coordinate space (VIEW_W × VIEW_H with preserveAspectRatio="none"),
 * so a rendered edge genuinely terminates at the node it belongs to.
 */
const SLOTS = [
  { left: 15, top: 24 },
  { left: 85, top: 22 },
  { left: 85, top: 64 },
  { left: 15, top: 66 },
] as const;

const VIEW_W = 520;
const VIEW_H = 226;
const CENTER = { x: VIEW_W / 2, y: VIEW_H * 0.45 };

/** Quadratic edge from the run hub to a satellite slot, bowed slightly. */
function edgePath(slot: (typeof SLOTS)[number]): string {
  const sx = (slot.left / 100) * VIEW_W;
  const sy = (slot.top / 100) * VIEW_H;
  const mx = (CENTER.x + sx) / 2;
  const my = (CENTER.y + sy) / 2;
  const dx = sx - CENTER.x;
  const dy = sy - CENTER.y;
  const len = Math.hypot(dx, dy) || 1;
  const cx = mx - (dy / len) * 14;
  const cy = my + (dx / len) * 14;
  return `M${CENTER.x},${CENTER.y} Q${cx.toFixed(1)},${cy.toFixed(1)} ${sx.toFixed(1)},${sy.toFixed(1)}`;
}

/**
 * LRM-783 home preview, rebuilt on real projection data (critique 2026-08-21):
 * the desktop layer renders the focused run as a hub and its actual
 * `active_assignments` as satellites — every drawn edge is a real
 * "agent is working on this run" relationship. Workspace aggregates moved to
 * an honest stat strip; no decorative pseudo-topology remains.
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
        <svg className="absolute inset-0 size-full" viewBox={`0 0 ${VIEW_W} ${VIEW_H}`} preserveAspectRatio="none" aria-hidden>
          <ellipse className="research-home-orbit" cx={CENTER.x} cy={CENTER.y} rx="194" ry="76" />
          {assignments.map((assignment, index) => (
            <path
              key={assignment.task_id || assignment.agent_id}
              className={`research-home-edge ${assignment.state === "running" ? "" : "research-home-edge-muted"}`}
              d={edgePath(SLOTS[index]!)}
            />
          ))}
        </svg>
        <div className="research-home-node research-home-node-main">
          <span className="w-[76px] min-w-0 overflow-hidden">
            <span className="block text-xs font-medium uppercase tracking-wide text-success">{t(($) => $.stage_short[stage as keyof typeof $.stage_short] ?? stage)}</span>
            <Tooltip>
              <TooltipTrigger render={<span className="mt-1 hidden truncate text-xs font-medium text-foreground sm:block" />}>
                {focus?.title || focus?.goal || t(($) => $.home_overview.constellation_empty)}
              </TooltipTrigger>
              <TooltipContent side="top">{focus?.title || focus?.goal}</TooltipContent>
            </Tooltip>
            <span className="mt-1 block text-xs tabular-nums text-muted-foreground">{progress ? t(($) => $.home_overview.tasks, { done: progress.task_completed, total: progress.task_total }) : "—"}</span>
          </span>
        </div>
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
                    className="research-home-node research-home-node-sat"
                    style={{ left: `${slot.left}%`, top: `${slot.top}%` }}
                    data-testid="research-home-constellation-agent"
                    aria-label={`${name} · ${stateLabel}`}
                  />
                }
              >
                <span className="w-[52px] min-w-0 overflow-hidden">
                  <span className="block truncate text-xs font-medium text-foreground">{name}</span>
                  <span className={`mt-0.5 block truncate text-[10px] ${running ? "text-brand" : "text-muted-foreground"}`}>{stateLabel}</span>
                </span>
              </TooltipTrigger>
              <TooltipContent side="top">{assignment.task_title || name}</TooltipContent>
            </Tooltip>
          );
        })}
        {focus && assignments.length === 0 ? (
          <p className="absolute inset-x-6 bottom-9 text-center text-xs text-muted-foreground">
            {t(($) => $.home_overview.assignments_idle)}
          </p>
        ) : null}
        <dl className="research-home-constellation-stats" data-testid="research-home-constellation-stats">
          {stats.map((item) => (
            <div key={item.key} className="flex min-w-0 items-baseline gap-1.5">
              <dd className="text-xs font-medium tabular-nums text-foreground">{item.value}</dd>
              <dt className="truncate text-[11px] text-muted-foreground">{item.label}</dt>
            </div>
          ))}
        </dl>
      </div>
      <div className="absolute inset-x-4 top-1/2 flex -translate-y-1/2 sm:hidden" aria-label={t(($) => $.home_overview.stage_progress)}>
        {STAGES.map((item, index) => <div key={item} className="relative flex min-w-0 flex-1 flex-col items-center text-center">{index > 0 ? <span className={`absolute right-1/2 top-3 h-px w-full ${index <= currentStage ? "bg-success/60" : "bg-border"}`} aria-hidden /> : null}<span className={`relative z-[1] grid size-7 place-items-center rounded-full border bg-card text-xs ${index === currentStage ? "border-brand text-brand" : index < currentStage ? "border-success text-success" : "border-border text-muted-foreground"}`}>{index + 1}</span><span className="mt-2 text-xs text-muted-foreground">{t(($) => $.stage_short[item])}</span></div>)}
      </div>
    </div>
  );
}
