"use client";

import { Check } from "lucide-react";
import type { CSSProperties } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  RESEARCH_STAGE_ORDER,
  resolveStageStepState,
  type ResearchStageId,
  type StageStepState,
} from "../lib/research-stages";

/**
 * LRM-1271/1291 — one semantic hue per stage, resolved through `tokens.css`
 * (`--research-stage-*`) rather than hex in JSX: the values land on gradient
 * `background-image` and on inline custom properties, and neither position can
 * carry a Tailwind `dark:` variant.
 *
 * `--stage` is the stage's own hue; `--stage-2` is a *different* hue used as the
 * second gradient stop on the CURRENT segment. It must never equal `--stage`:
 * with identical stops the current band paints as one flat color and becomes
 * indistinguishable from a `done` segment, which is exactly what the first
 * gate-shot run showed for define/verify/deliver.
 *
 * Explore is pinned to the spec's violet → fuchsia. The others lean into the
 * next stage's hue so the band reads as flowing forward; `deliver` is terminal
 * and closes back to define (emerald → cyan, both cool, no clash).
 */
const STAGE_HUES: Record<ResearchStageId, { from: string; to: string }> = {
  s1_plan: {
    from: "var(--research-stage-define)",
    to: "var(--research-stage-explore)",
  },
  s2_sources: {
    from: "var(--research-stage-explore)",
    to: "var(--research-stage-explore-2)",
  },
  s3_validation: {
    from: "var(--research-stage-verify)",
    to: "var(--research-stage-deliver)",
  },
  s4_delivery: {
    from: "var(--research-stage-deliver)",
    to: "var(--research-stage-define)",
  },
};

function stageVars(stage: ResearchStageId): CSSProperties {
  const hue = STAGE_HUES[stage];
  return {
    "--stage": hue.from,
    "--stage-2": hue.to,
  } as CSSProperties;
}

/**
 * The 9px continuous band. Segments abut (no gap, `flex-1`) so the four stages
 * read as one track; only the outer ends are rounded.
 *
 * State is never carried by hue alone (WCAG 1.4.1):
 * - done     → solid fill
 * - current  → two-stop gradient + a single sheen overlay
 * - upcoming → 45° hatch at low alpha, i.e. a different *pattern*, not just a
 *              paler version of the same color
 */
function StageBand({
  state,
  isFirst,
  isLast,
}: {
  state: StageStepState;
  isFirst: boolean;
  isLast: boolean;
}) {
  return (
    <span
      aria-hidden
      data-stage-band={state}
      className={cn(
        "relative block h-[9px] w-full overflow-hidden",
        isFirst && "rounded-l-full",
        isLast && "rounded-r-full",
        state === "done" && "bg-[var(--stage)]",
        state === "current" &&
          "bg-[linear-gradient(90deg,var(--stage)_0%,var(--stage-2)_100%)]",
        state === "upcoming" &&
          "bg-[repeating-linear-gradient(135deg,color-mix(in_oklch,var(--stage)_38%,transparent)_0_3px,transparent_3px_6px)]",
      )}
    >
      {state === "current" ? (
        <span
          aria-hidden
          data-stage-sheen=""
          className="animate-research-stage-sheen absolute inset-0 block"
        />
      ) : null}
    </span>
  );
}

/**
 * Glyphs differ by shape as well as color so `done` / `current` / `upcoming`
 * survive greyscale and color-blind viewing:
 * - done     → filled disc + check
 * - current  → 28px ring with a background-colored outline and a solid center
 *              dot (the frozen spec's "28px 白描边 ring / 中心实点")
 * - upcoming → hollow disc, stage-hued stroke only
 */
function StepGlyph({ state }: { state: StageStepState }) {
  if (state === "done") {
    return (
      <span
        className="flex size-5 items-center justify-center rounded-full bg-[var(--stage)] text-background"
        aria-hidden
      >
        <Check className="size-3 stroke-[2.5]" />
      </span>
    );
  }
  if (state === "current") {
    return (
      <span
        data-stage-current-ring=""
        className="flex size-7 items-center justify-center rounded-full border-2 border-[var(--stage)] ring-2 ring-background"
        aria-hidden
      >
        <span className="size-2.5 rounded-full bg-[linear-gradient(90deg,var(--stage)_0%,var(--stage-2)_100%)]" />
      </span>
    );
  }
  return (
    <span
      className="flex size-5 items-center justify-center rounded-full border-2 border-[color-mix(in_oklch,var(--stage)_55%,transparent)]"
      aria-hidden
    />
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
        className={cn(
          // Four equal segments at every width: with S1–S4 short labels the
          // narrow track fits 360 without a scroll strip, so the band stays
          // continuous instead of being cut by an overflow edge.
          "flex gap-0 px-3 py-2 md:px-4",
        )}
      >
        {RESEARCH_STAGE_ORDER.map((stage, index) => {
          const state = resolveStageStepState(stage, currentStage, sessionStatus);
          const label = t(($) => $.stage[stage]);
          const shortLabel = t(($) => $.stage_short[stage]);
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
              data-stage={stage}
              style={stageVars(stage)}
              className="flex min-w-0 flex-1 flex-col gap-1.5"
            >
              <StageBand
                state={state}
                isFirst={index === 0}
                isLast={index === RESEARCH_STAGE_ORDER.length - 1}
              />
              <button
                type="button"
                disabled={!clickable}
                onClick={() => onSelectStage?.(stage)}
                className={cn(
                  "relative z-[1] flex max-w-full flex-col items-center gap-0.5 rounded-md px-0.5 py-1 text-center transition-colors md:flex-row md:gap-2 md:px-1.5 md:text-left",
                  clickable &&
                    "hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30",
                  !clickable && "cursor-default",
                  state === "current" &&
                    "bg-[color-mix(in_oklch,var(--stage)_10%,transparent)]",
                )}
                aria-current={state === "current" ? "step" : undefined}
                aria-label={`${label} — ${stateLabel}`}
              >
                <span className="flex h-7 shrink-0 items-center justify-center">
                  <StepGlyph state={state} />
                </span>
                <span className="min-w-0 text-center md:text-left">
                  {/* Narrow shows the real localized S1–S4 abbreviation, while
                      the accessible button name keeps the complete stage name.
                      The state label remains visibly rendered at every width:
                      it may not be hidden behind an aria-label or tooltip. */}
                  <span
                    aria-hidden
                    className={cn(
                      "block truncate tracking-wide text-xs md:hidden",
                      state === "current" && "font-medium text-foreground",
                      state === "done" && "font-mono font-normal text-foreground",
                      state === "upcoming" &&
                        "font-mono font-normal text-muted-foreground",
                    )}
                  >
                    {shortLabel}
                  </span>
                  <span
                    className={cn(
                      "hidden truncate tracking-wide text-xs md:block",
                      state === "current" && "font-medium text-foreground",
                      state === "done" && "font-mono font-normal text-foreground",
                      state === "upcoming" &&
                        "font-mono font-normal text-muted-foreground",
                    )}
                  >
                    {label}
                  </span>
                  {/* State redundancy stays visible in the 360–767 layout;
                      glyph/pattern and text together distinguish every state. */}
                  <span
                    data-stage-state-text=""
                    className={cn(
                      "mt-0.5 block whitespace-nowrap text-xs leading-tight font-medium",
                      state === "current"
                        ? "text-[var(--stage)]"
                        : "text-muted-foreground",
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
      <p className="font-mono text-xs tracking-wider text-muted-foreground uppercase">
        {label}
      </p>
    </div>
  );
}
