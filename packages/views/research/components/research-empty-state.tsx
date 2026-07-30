"use client";

import { Sparkles } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n/use-t";

const EXAMPLE_KEYS = ["q1", "q2", "q3", "q4"] as const;

type ResearchEmptyStateProps = {
  onSelectExample: (goal: string) => void;
  onStart: () => void;
};

export function ResearchEmptyState({
  onSelectExample,
  onStart,
}: ResearchEmptyStateProps) {
  const { t } = useT("research");

  return (
    <section
      aria-label={t(($) => $.empty_title)}
      className="flex w-full max-w-2xl flex-col items-center gap-4 rounded-lg border border-dashed px-4 py-8 text-center"
    >
      <div className="flex h-10 w-10 items-center justify-center rounded-full bg-primary/10 text-primary">
        <Sparkles className="h-5 w-5" aria-hidden />
      </div>
      <div className="space-y-1">
        <h2 className="text-base font-medium">{t(($) => $.empty_title)}</h2>
        <p className="text-sm text-muted-foreground">
          {t(($) => $.empty_desc)}
        </p>
      </div>
      <div className="flex w-full flex-col items-stretch gap-2">
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
      <Button onClick={onStart}>{t(($) => $.empty_cta)}</Button>
    </section>
  );
}
