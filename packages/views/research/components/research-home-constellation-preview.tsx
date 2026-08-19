"use client";

import type { ResearchSession } from "@multica/core/types/research";
import { useT } from "../../i18n/use-t";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import {
  activeResearchSessions,
  knownResearchAttentionKind,
  selectedResearchSession,
} from "../lib/research-home-selection";

const STAGES = ["s1_plan", "s2_sources", "s3_validation", "s4_delivery"] as const;

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

  const satellite = (className: string, label: string, value: string | number) => (
    <div className={`research-home-node ${className}`}>
      <span><span className="block text-xs font-medium text-foreground">{value}</span><span className="mt-0.5 block text-xs text-muted-foreground">{label}</span></span>
    </div>
  );

  return (
    <div className="research-home-constellation" data-testid="research-home-constellation" aria-label={t(($) => $.home_overview.constellation_label)}>
      <div className="hidden sm:block">
      <svg className="absolute inset-0 size-full" viewBox="0 0 520 226" preserveAspectRatio="none" aria-hidden>
        <ellipse className="research-home-orbit" cx="260" cy="113" rx="194" ry="88" />
        <path className="research-home-edge" d="M230 92 C 190 70, 145 53, 94 47" />
        <path className="research-home-edge research-home-edge-support" d="M292 91 C 348 62, 395 50, 435 45" />
        <path className="research-home-edge research-home-edge-support" d="M294 137 C 348 157, 392 176, 434 184" />
        <path className={`research-home-edge ${attention > 0 ? "research-home-edge-risk" : ""}`} d="M227 138 C 179 160, 138 177, 92 184" />
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
      {satellite("research-home-node-a", t(($) => $.home_overview.active), active.length)}
      {satellite("research-home-node-b", t(($) => $.home_overview.agent_short), agents || "—")}
      {satellite("research-home-node-c", t(($) => $.home_overview.evidence_short), evidence || "—")}
      {satellite("research-home-node-d", attention > 0 ? t(($) => $.home_overview.attention) : t(($) => $.home_overview.open_questions_short), attention || questions || "—")}
      </div>
      <div className="absolute inset-x-4 top-1/2 flex -translate-y-1/2 sm:hidden" aria-label={t(($) => $.home_overview.stage_progress)}>
        {STAGES.map((item, index) => <div key={item} className="relative flex min-w-0 flex-1 flex-col items-center text-center">{index > 0 ? <span className={`absolute right-1/2 top-3 h-px w-full ${index <= currentStage ? "bg-success/60" : "bg-border"}`} aria-hidden /> : null}<span className={`relative z-[1] grid size-7 place-items-center rounded-full border bg-card text-xs ${index === currentStage ? "border-brand text-brand" : index < currentStage ? "border-success text-success" : "border-border text-muted-foreground"}`}>{index + 1}</span><span className="mt-2 text-xs text-muted-foreground">{t(($) => $.stage_short[item])}</span></div>)}
      </div>
    </div>
  );
}
