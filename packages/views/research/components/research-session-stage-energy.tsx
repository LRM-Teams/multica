"use client";

import { Check } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  RESEARCH_STAGE_ORDER,
  resolveStageStepState,
  type StageStepState,
} from "../lib/research-stages";

function laneVar(index: number): string {
  return `var(--research-lane-${index + 1})`;
}

function Segment({
  state,
  laneIndex,
}: {
  state: StageStepState;
  laneIndex: number;
}) {
  const color = laneVar(laneIndex);

  if (state === "done") {
    return (
      <span
        data-stage-state="done"
        aria-hidden
        className="relative flex h-1.5 w-5 items-center justify-center rounded-full"
        style={{ backgroundColor: color }}
      >
        <Check className="size-2 text-background" strokeWidth={3} aria-hidden />
      </span>
    );
  }

  if (state === "current") {
    return (
      <span
        data-stage-state="current"
        aria-hidden
        className="relative flex h-1.5 w-5 items-center justify-center rounded-full"
        style={{
          backgroundColor: color,
          boxShadow: `0 0 6px color-mix(in oklab, ${color} 55%, transparent)`,
        }}
      >
        <span
          className={cn(
            "size-1.5 rounded-full bg-background ring-1 ring-background/90",
            "motion-safe:group-hover:animate-pulse motion-safe:group-focus-within:animate-pulse",
            "motion-reduce:animate-none",
          )}
        />
      </span>
    );
  }

  return (
    <span
      data-stage-state="upcoming"
      aria-hidden
      className="h-1.5 w-5 rounded-full border bg-transparent"
      style={{ borderColor: color }}
    />
  );
}

/**
 * LRM-1285 / LRM-1279 — mini S1–S4 energy badge for the home list row.
 * Non-interactive display only (no new tab stop). Host: ResearchSessionRow.
 */
export function ResearchSessionStageEnergy({
  currentStage,
  sessionStatus,
  className,
}: {
  currentStage: string;
  sessionStatus: string;
  className?: string;
}) {
  const { t } = useT("research");
  const stageLabel = t(
    ($) => $.stage[currentStage as keyof typeof $.stage] ?? currentStage,
  );
  const statusLabel = t(
    ($) => $.status[sessionStatus as keyof typeof $.status] ?? sessionStatus,
  );

  const states = RESEARCH_STAGE_ORDER.map((stage) =>
    resolveStageStepState(stage, currentStage, sessionStatus),
  );
  const doneCount = states.filter((s) => s === "done").length;

  const ariaLabel = t(($) => $.list.stage_energy_aria, {
    stage: stageLabel,
    status: statusLabel,
    done: doneCount,
  });

  return (
    <span
      data-testid="research-session-stage-energy"
      role="img"
      aria-label={ariaLabel}
      className={cn("inline-flex w-28 shrink-0 flex-col gap-0.5", className)}
    >
      <span className="truncate text-xs font-medium text-foreground/80">{stageLabel}</span>
      <span className="flex items-center gap-1" aria-hidden>
        {RESEARCH_STAGE_ORDER.map((stage, index) => (
          <Segment key={stage} state={states[index]!} laneIndex={index} />
        ))}
      </span>
    </span>
  );
}
