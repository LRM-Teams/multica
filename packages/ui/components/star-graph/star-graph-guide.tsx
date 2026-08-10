"use client";

/**
 * StarGraphGuide — three-step, skippable D5 on-boarding (LRM-1496).
 *
 * Stateless, *controlled* presentation component in `@multica/ui`. It walks a
 * first-time user through the three ideas in plain language:
 *   1. 成果等级 — big nodes are merged results, small dots are working Agents;
 *   2. S Agent — the small dots are the running Agents and their states;
 *   3. 实线 / 虚线与聚类边界 — which relation a solid / dashed line means and
 *      that dashed cluster borders wrap real members.
 *
 * The parent owns the current `step`, skips, and finish, so the "shown once"
 * persistence and the help re-open entry can live in `packages/views`
 * (guide-persistence.ts). This component touches no storage and no domain
 * logic.
 */

import { cn } from "@multica/ui/lib/utils";

import {
  STAR_GRAPH_GUIDE_STEPS,
} from "./guide-steps";

export interface StarGraphGuideProps {
  /** Controlled 0-based step index. */
  step: number;
  /** Total steps (defaults to STAR_GRAPH_GUIDE_STEPS length). */
  totalSteps?: number;
  onNext: () => void;
  onBack?: () => void;
  onClose: () => void;
  onFinish: () => void;
  /** Optional intro line shown above the step. */
  intro?: string;
  className?: string;
}

export function StarGraphGuide({
  step,
  totalSteps = STAR_GRAPH_GUIDE_STEPS.length,
  onNext,
  onBack,
  onClose,
  onFinish,
  intro,
  className,
}: StarGraphGuideProps) {
  const safeTotal = Math.max(totalSteps, 1);
  const current = Math.min(Math.max(step, 0), safeTotal - 1);
  const isLast = current === safeTotal - 1;
  // STAR_GRAPH_GUIDE_STEPS is a statically non-empty literal; `current` is
  // clamped to [0, length-1], so the index is always defined.
  const stepDef = STAR_GRAPH_GUIDE_STEPS[Math.min(current, STAR_GRAPH_GUIDE_STEPS.length - 1)]!;

  const goNext = () => {
    if (isLast) {
      onFinish();
      return;
    }
    onNext();
  };

  return (
    <section
      data-testid="star-graph-guide"
      aria-label="星图引导"
      className={cn("sg-guide", className)}
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-xs font-bold tracking-[0.14em] text-muted-foreground">
          星图引导 · {current + 1}/{safeTotal}
        </span>
        <button
          type="button"
          data-testid="guide-skip"
          className="rounded px-2 py-1 text-xs text-muted-foreground"
          onClick={onClose}
        >
          跳过
        </button>
      </div>

      {intro && <p className="mb-2 text-xs text-muted-foreground">{intro}</p>}

      <div className="sg-step">
        <div className="text-sm font-bold">{stepDef.title}</div>
        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
          {stepDef.body}
        </p>
      </div>

      <div className="mt-3 flex items-center justify-between gap-2">
        <button
          type="button"
          data-testid="guide-back"
          className="rounded px-2 py-1 text-xs text-muted-foreground disabled:opacity-40"
          onClick={onBack}
          disabled={current === 0}
        >
          上一步
        </button>
        <button
          type="button"
          data-testid="guide-next"
          className="rounded bg-primary px-3 py-1 text-xs font-semibold text-primary-foreground"
          onClick={goNext}
        >
          {isLast ? "完成" : "下一步"}
        </button>
      </div>
    </section>
  );
}
