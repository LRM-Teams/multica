"use client";

import { Button } from "@multica/ui/components/ui/button";
import { FileSearch, Network, ShieldCheck, Users } from "lucide-react";
import { useT } from "../../i18n/use-t";

const EXAMPLE_KEYS = ["q1", "q2", "q3", "q4"] as const;

type ResearchEmptyStateProps = {
  onSelectExample: (goal: string) => void;
  onStart: () => void;
};

function EmptyResearchPath() {
  const { t } = useT("research");
  const steps = [
    [FileSearch, t(($) => $.home_empty.ask)],
    [Users, t(($) => $.home_empty.assign)],
    [ShieldCheck, t(($) => $.home_empty.verify)],
    [Network, t(($) => $.home_empty.deliver)],
  ] as const;
  return (
    <div className="mb-3 grid w-full max-w-xl grid-cols-2 gap-2 sm:grid-cols-4" aria-label={t(($) => $.home_empty.path)}>
      {steps.map(([Icon, label], index) => <div key={label} className="relative flex flex-col items-center gap-2 rounded-lg border border-border/75 bg-card/70 px-2 py-3 text-xs text-muted-foreground">{index > 0 ? <span className="absolute right-1/2 top-5 hidden h-px w-full bg-border sm:block" aria-hidden /> : null}<span className="relative z-[1] flex size-8 items-center justify-center rounded-full border border-brand/40 bg-card text-brand"><Icon className="size-4" aria-hidden /></span><span className="relative z-[1] text-center">{label}</span></div>)}
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
      <EmptyResearchPath />
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
