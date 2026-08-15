"use client";

import type { ReactNode } from "react";
import { Compass } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/**
 * LRM-783 / LRM-784 lock — brand-hero façade for the research home.
 * LRM-1106: width owned by workbench shell (no child max-width pin).
 * LRM-1144 Δ1: atmosphere lives on the workbench, not inside the hero.
 * LRM-1144 Δ2: hero_desc sinks under the title as one subtitle line (no max-w-[36rem]).
 */
export function ResearchHomeHero({
  children,
  preview,
  className,
}: {
  children?: ReactNode;
  preview?: ReactNode;
  className?: string;
}) {
  const { t } = useT("research");

  return (
    <section
      className={cn("relative w-full", className)}
      data-testid="research-home-hero"
      aria-label={t(($) => $.home.composer_label)}
    >
      <div className="research-home-launch grid lg:grid-cols-[minmax(0,1.15fr)_minmax(380px,0.85fr)]">
        <div className="relative flex min-w-0 flex-col gap-3 overflow-hidden p-4 md:p-5 lg:p-6">
        <div className="flex items-center gap-2.5">
          <span
            className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-brand/12 text-brand"
            aria-hidden
          >
            <Compass className="size-4" strokeWidth={2} />
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="line-clamp-2 text-sm font-medium tracking-tight text-foreground sm:text-base md:text-xl">
              {t(($) => $.home.hero_title)}
            </h2>
            <p className="mt-1 hidden line-clamp-1 text-xs text-muted-foreground md:block">
              {t(($) => $.home.hero_desc)}
            </p>
          </div>
        </div>
        {children}
        </div>
        {preview}
      </div>
    </section>
  );
}
