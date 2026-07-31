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
        className="flex size-5 items-center justify-center rounded-full bg-brand text-brand-foreground ring-2 ring-brand/30"
        aria-hidden
      >
        <span className="size-1.5 rounded-full bg-current" />
      </span>
    );
  }
  return (
    <span
      className="flex size-5 items-center justify-center rounded-full border border-border bg-muted/40"
      aria-hidden
    >
      <span className="size-1.5 rounded-full bg-muted-foreground/50" />
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
      className="shrink-0 border-b bg-background/70"
    >
      <ol
        className={cn(
          // Narrow: horizontal scroll strip; sm+: full-width equal steps.
          "flex gap-0 overflow-x-auto px-3 py-2 sm:overflow-visible sm:px-4",
          "snap-x snap-mandatory sm:snap-none",
          "[scrollbar-width:none] [&::-webkit-scrollbar]:hidden",
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
              className={cn(
                "relative flex min-w-[7.5rem] flex-1 snap-start items-center sm:min-w-0",
                index < RESEARCH_STAGE_ORDER.length - 1 && "pr-2",
              )}
            >
              {index < RESEARCH_STAGE_ORDER.length - 1 ? (
                <span
                  aria-hidden
                  className={cn(
                    "pointer-events-none absolute top-[0.625rem] right-0 left-8 h-px sm:left-10",
                    state === "done" ? "bg-success/50" : "bg-border",
                  )}
                />
              ) : null}
              <button
                type="button"
                disabled={!clickable}
                onClick={() => onSelectStage?.(stage)}
                className={cn(
                  "relative z-[1] flex max-w-full items-center gap-2 rounded-md px-1.5 py-1 text-left transition-colors",
                  clickable && "hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30",
                  !clickable && "cursor-default",
                  state === "current" && "bg-brand/8",
                )}
                aria-current={state === "current" ? "step" : undefined}
                aria-label={`${label} — ${stateLabel}`}
              >
                <StepGlyph state={state} />
                <span
                  className={cn(
                    "truncate font-mono text-[11px] tracking-wide",
                    state === "current" && "font-semibold text-foreground",
                    state === "done" && "text-foreground/80",
                    state === "upcoming" && "text-muted-foreground",
                  )}
                >
                  {label}
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
