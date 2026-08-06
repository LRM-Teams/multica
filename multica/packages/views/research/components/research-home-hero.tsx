"use client";

import type { ReactNode } from "react";
import { Compass } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { ResearchShellAtmosphere } from "./research-shell-atmosphere";

/**
 * LRM-783 / LRM-784 lock — brand-hero façade for the research home:
 * compass glyph + 「调研」wordmark + one value line + optional composer slot.
 */
export function ResearchHomeHero({
  children,
  className,
}: {
  children?: ReactNode;
  className?: string;
}) {
  const { t } = useT("research");

  return (
    <section
      className={cn("relative w-full max-w-3xl", className)}
      data-testid="research-home-hero"
      aria-label={t(($) => $.home.composer_label)}
    >
      <ResearchShellAtmosphere className="-top-2" heightClassName="h-[220px]" />
      <div className="relative flex flex-col gap-3 sm:gap-3.5">
        <div className="flex items-center gap-2.5">
          <span
            className="flex size-9 shrink-0 items-center justify-center rounded-[9px] bg-brand/12 text-brand"
            aria-hidden
          >
            <Compass className="size-[19px]" strokeWidth={2} />
          </span>
          <h1 className="text-[22px] font-semibold tracking-tight text-foreground sm:text-[26px]">
            {t(($) => $.home.hero_title)}
          </h1>
        </div>
        <p className="max-w-[36rem] text-[13px] leading-snug text-muted-foreground sm:text-[13.5px] sm:leading-relaxed">
          {t(($) => $.home.hero_desc)}
        </p>
        {children}
      </div>
    </section>
  );
}
