"use client";

import { Check } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  RESEARCH_STAGE_ORDER,
  resolveStageStepState,
  type ResearchStageId,
  type StageStepState,
} from "../lib/research-stages";

type StageTone = {
  fill: string;
  gradient: string;
  outline: string;
};

/** Theme-aware research lane tokens keep the energy rail legible in light and dark mode. */
const STAGE_TONES: Record<ResearchStageId, StageTone> = {
  s1_plan: {
    fill: "bg-[var(--research-lane-1)]",
    gradient: "from-[var(--research-lane-3)] to-[var(--research-lane-1)]",
    outline: "text-[var(--research-lane-1)]",
  },
  s2_sources: {
    fill: "bg-[var(--research-lane-4)]",
    gradient: "from-[var(--research-lane-4)] to-brand",
    outline: "text-[var(--research-lane-4)]",
  },
  s3_validation: {
    fill: "bg-[var(--research-lane-5)]",
    gradient: "from-[var(--research-lane-5)] to-[var(--research-lane-2)]",
    outline: "text-[var(--research-lane-2)]",
  },
  s4_delivery: {
    fill: "bg-success",
    gradient: "from-success to-[var(--research-lane-1)]",
    outline: "text-success",
  },
};

function StepGlyph({ state, tone }: { state: StageStepState; tone: StageTone }) {
  if (state === "done") {
    return (
      <span
        data-stage-glyph="done"
        className={cn(
          "flex size-6 items-center justify-center rounded-full text-background shadow-sm",
          tone.fill,
        )}
        aria-hidden
      >
        <Check className="size-3.5 stroke-[2.5]" />
      </span>
    );
  }

  if (state === "current") {
    return (
      <span
        data-stage-glyph="current"
        className={cn(
          "flex size-7 items-center justify-center rounded-full border-2 border-background ring-1 ring-foreground/20",
          tone.fill,
        )}
        aria-hidden
      >
        <span className="size-2 rounded-full bg-background" />
      </span>
    );
  }

  return (
    <span
      data-stage-glyph="upcoming"
      className={cn(
        "flex size-6 items-center justify-center rounded-full border-2 border-current bg-background",
        tone.outline,
      )}
      aria-hidden
    >
      <span className="size-1.5 rounded-full bg-current" />
    </span>
  );
}

export function ResearchStageTimeline({
  currentStage,
  sessionStatus,
  onSelectStage,
}: {
  currentStage: string;
  sessionStatus: string;
  /** Click a stage (typically done/current) to anchor the chat message area. */
  onSelectStage?: (stage: string) => void;
}) {
  const { t } = useT("research");

  return (
    <nav
      aria-label={t(($) => $.timeline.label)}
      data-testid="research-stage-timeline"
      className="relative z-[1] shrink-0 border-b border-border/55 bg-background/55 backdrop-blur-sm"
    >
      <ol
        data-testid="research-stage-energy-rail"
        className="grid grid-cols-4 overflow-hidden px-3 pt-3 pb-2 md:px-4"
      >
        {RESEARCH_STAGE_ORDER.map((stage, index) => {
          const state = resolveStageStepState(stage, currentStage, sessionStatus);
          const label = t(($) => $.stage[stage]);
          const tone = STAGE_TONES[stage];
          const clickable = state !== "upcoming" && Boolean(onSelectStage);
          const stateLabel =
            state === "done"
              ? t(($) => $.timeline.done)
              : state === "current"
                ? t(($) => $.timeline.current)
                : t(($) => $.timeline.upcoming);

          return (
            <li
              key={stage}
              data-stage-state={state}
              data-stage-energy-segment
              data-stage-energy-state={state}
              className="relative min-w-0"
            >
              <span
                aria-hidden
                className={cn(
                  "absolute top-0 right-0 left-0 h-2 bg-gradient-to-r",
                  state === "upcoming" ? "opacity-35" : "opacity-100",
                  tone.gradient,
                )}
              >
                {state === "current" ? (
                  <span
                    data-stage-flow="current"
                    className="absolute inset-0 bg-gradient-to-r from-transparent via-background/70 to-transparent animate-pulse motion-reduce:animate-none"
                  />
                ) : (
                  <span data-stage-flow="none" />
                )}
              </span>
              <button
                type="button"
                disabled={!clickable}
                onClick={() => onSelectStage?.(stage)}
                className={cn(
                  "relative flex min-h-12 w-full min-w-0 items-center gap-1.5 pt-3 text-left transition-[background-color,box-shadow] duration-200 md:justify-center md:gap-2",
                  clickable &&
                    "rounded-md hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40",
                  !clickable && "cursor-default",
                  state === "current" && "rounded-md bg-muted/45",
                )}
                aria-current={state === "current" ? "step" : undefined}
                aria-label={`${label} — ${stateLabel}`}
              >
                <StepGlyph state={state} tone={tone} />
                <span className="min-w-0">
                  <span
                    data-stage-short-label
                    className="block text-xs font-medium text-foreground md:hidden"
                  >
                    {`S${index + 1}`}
                  </span>
                  <span
                    data-stage-label
                    className={cn(
                      "hidden truncate text-xs md:block",
                      state === "current" && "font-medium text-foreground",
                      state === "done" && "font-normal text-foreground",
                      state === "upcoming" && "font-normal text-muted-foreground",
                    )}
                  >
                    {label}
                  </span>
                  <span
                    className={cn(
                      "mt-0.5 hidden text-xs md:block",
                      state === "upcoming" ? "text-muted-foreground" : "text-foreground",
                    )}
                  >
                    {stateLabel}
                  </span>
                </span>
                {state === "done" ? (
                  <span className="sr-only">{t(($) => $.timeline.done_feedback)}</span>
                ) : null}
              </button>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

/** Compact stage divider rendered inside the chat scroll area for anchoring. */
export function ResearchStageChatMarker({
  stage,
  label,
}: {
  stage: string;
  label: string;
}) {
  return (
    <div
      id={`research-stage-${stage}`}
      data-research-stage={stage}
      className="-mx-1 border-y border-dashed bg-muted/30 px-2 py-1.5"
    >
      <p className="font-mono text-[10px] tracking-wider text-muted-foreground uppercase">
        {label}
      </p>
    </div>
  );
}
