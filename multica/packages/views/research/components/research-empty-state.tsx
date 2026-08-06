"use client";

import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n/use-t";

const EXAMPLE_KEYS = ["q1", "q2", "q3", "q4"] as const;

type ResearchEmptyStateProps = {
  onSelectExample: (goal: string) => void;
  onStart: () => void;
};

/** LRM-783 / LRM-784 — designed empty with mini-canvas (goal + dashed ghosts). */
function EmptyMiniCanvas({
  goalLabel,
  probeLabel,
  sourceLabel,
}: {
  goalLabel: string;
  probeLabel: string;
  sourceLabel: string;
}) {
  return (
    <div
      aria-hidden
      className="relative mb-4 h-28 w-[270px] rounded-xl sm:h-32 sm:w-[300px]"
      style={{
        backgroundImage:
          "radial-gradient(circle, color-mix(in oklab, var(--foreground) 10%, transparent) 1px, transparent 1.5px)",
        backgroundSize: "24px 24px",
      }}
    >
      <svg
        className="absolute inset-0 size-full"
        viewBox="0 0 300 128"
        fill="none"
      >
        <path
          d="M150 50 C 122 68, 100 76, 78 88"
          stroke="var(--border)"
          strokeWidth="1.5"
          strokeDasharray="4 4"
        />
        <path
          d="M150 50 C 178 68, 200 76, 222 88"
          stroke="var(--border)"
          strokeWidth="1.5"
          strokeDasharray="4 4"
        />
      </svg>
      <div className="absolute left-1/2 top-5 -translate-x-1/2 rounded-[9px] bg-brand px-2.5 py-1.5 text-[11.5px] font-semibold text-brand-foreground ring-2 ring-brand/45">
        {goalLabel}
      </div>
      <div className="absolute left-9 top-[72px] rounded-lg border border-dashed border-input bg-background px-2 py-1 text-[10.5px] text-muted-foreground">
        {probeLabel}
      </div>
      <div className="absolute right-9 top-[72px] rounded-lg border border-dashed border-input bg-background px-2 py-1 text-[10.5px] text-muted-foreground">
        {sourceLabel}
      </div>
    </div>
  );
}

export function ResearchEmptyState({
  onSelectExample,
  onStart,
}: ResearchEmptyStateProps) {
  const { t } = useT("research");

  return (
    <section
      aria-label={t(($) => $.empty_title)}
      className="flex w-full max-w-2xl flex-col items-center gap-3 px-2 py-8 text-center sm:py-10"
      data-testid="research-empty-state"
    >
      <EmptyMiniCanvas
        goalLabel={t(($) => $.node.goal)}
        probeLabel={t(($) => $.node.probe)}
        sourceLabel={t(($) => $.logic.lane.source)}
      />
      <div className="space-y-1">
        <h2 className="text-sm font-semibold">{t(($) => $.empty_title)}</h2>
        <p className="mx-auto max-w-[22rem] text-[12.5px] leading-relaxed text-muted-foreground">
          {t(($) => $.empty_desc)}
        </p>
      </div>
      <div className="flex w-full max-w-md flex-col items-stretch gap-2 pt-1">
        <p className="text-xs font-medium text-muted-foreground">
          {t(($) => $.empty_examples_label)}
        </p>
        {EXAMPLE_KEYS.map((key) => {
          const example = t(($) => $.empty_examples[key]);
          return (
            <button
              key={key}
              type="button"
              onClick={() => onSelectExample(example)}
              className="w-full rounded-md border bg-background px-3 py-2 text-left text-sm break-words transition-colors hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {example}
            </button>
          );
        })}
      </div>
      <Button onClick={onStart} className="mt-1 rounded-full bg-brand text-brand-foreground hover:bg-brand/90">
        {t(($) => $.empty_cta)}
      </Button>
    </section>
  );
}
