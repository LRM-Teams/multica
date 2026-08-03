"use client";

import { Check } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  RESEARCH_STAGE_ORDER,
  resolveStageStepState,
  type StageStepState,
} from "../lib/research-stages";

function StepGlyph({ state }: { state: StageStepState }) {
  if (state === "done") {
    return (
      <span
        className="flex size-5 items-center justify-center rounded-full bg-success text-background"
        aria-hidden
      >
        <Check className="size-3 stroke-[2.5]" />
      </span>
    );
  }
  if (state === "current") {
    return (
      <span
        className="flex size-6 items-center justify-center rounded-full bg-brand text-brand-foreground shadow-sm ring-2 ring-brand/35"
        aria-hidden
      >
        <span className="size-1.5 rounded-full bg-current" />
      </span>
    );
  }
  return (
    <span
      className="flex size-5 items-center justify-center rounded-full border border-border/80 bg-muted/30 opacity-70"
      aria-hidden
    >
      <span className="size-1.5 rounded-full bg-muted-foreground/40" />
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
      className="min-w-0 flex-1"
    >
      <ol
        className={cn(
          // The shared header owns the surface; steps wrap rather than overflow on narrow screens.
          "flex flex-wrap gap-y-1 px-0 py-0",
        )}
      >
        {RESEARCH_STAGE_ORDER.map((stage, index) => {
          const state = resolveStageStepState(stage, currentStage, sessionStatus);
          const label = t(($) => $.stage[stage]);
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
              className={cn(
                "relative flex min-w-[4rem] flex-1 items-center sm:min-w-[6.5rem]",
                index < RESEARCH_STAGE_ORDER.length - 1 && "pr-1.5 sm:pr-2",
                state === "upcoming" && "opacity-75",
              )}
            >
              {index < RESEARCH_STAGE_ORDER.length - 1 ? (
                <span
                  aria-hidden
                  className={cn(
                    "pointer-events-none absolute top-[0.7rem] right-0 left-9 h-px sm:left-11",
                    state === "done" ? "bg-success/50" : "bg-border/80",
                    state === "current" && "bg-gradient-to-r from-brand/50 to-border/80",
                  )}
                />
              ) : null}
              <button
                type="button"
                disabled={!clickable}
                onClick={() => onSelectStage?.(stage)}
                className={cn(
                  "relative z-[1] flex max-w-full items-center gap-2 rounded-md px-1.5 py-1 text-left transition-colors",
                  clickable &&
                    "hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30",
                  !clickable && "cursor-default",
                  state === "current" && "bg-brand/10 ring-1 ring-brand/20",
                )}
                aria-current={state === "current" ? "step" : undefined}
                aria-label={`${label} — ${stateLabel}`}
              >
                <StepGlyph state={state} />
                <span className="min-w-0">
                  <span
                    className={cn(
                      "block truncate tracking-wide",
                      state === "current" && "text-xs font-semibold text-foreground",
                      state === "done" && "font-mono text-[11px] text-foreground/75",
                      state === "upcoming" &&
                        "font-mono text-[11px] text-muted-foreground/80",
                    )}
                  >
                    {label}
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
