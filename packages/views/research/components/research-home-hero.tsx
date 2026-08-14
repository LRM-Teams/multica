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
  aside,
  className,
}: {
  children?: ReactNode;
  aside?: ReactNode;
  className?: string;
}) {
  const { t } = useT("research");

  return (
    <section
      className={cn("relative w-full", className)}
      data-testid="research-home-hero"
      aria-label={t(($) => $.home.composer_label)}
    >
      <div className="relative flex flex-col gap-3 md:gap-3.5">
        <div className="flex items-start gap-2.5">
          <span
            className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-[9px] bg-brand/12 text-brand"
            aria-hidden
          >
            <Compass className="size-[19px]" strokeWidth={2} />
          </span>
          <div className="min-w-0 flex-1">
            <h1 className="text-xl font-medium tracking-tight text-foreground md:text-2xl">
              {t(($) => $.home.hero_title)}
            </h1>
            <p className="mt-0.5 text-sm leading-snug text-muted-foreground md:line-clamp-1 md:leading-relaxed">
              {t(($) => $.home.hero_desc)}
            </p>
          </div>
        </div>
        {aside ? (
          <div className="grid items-stretch gap-3 lg:grid-cols-[minmax(0,1.65fr)_minmax(280px,0.75fr)]">
            {children}
            {aside}
          </div>
        ) : children}
      </div>
    </section>
  );
}
