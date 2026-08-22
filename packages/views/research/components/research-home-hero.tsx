"use client";

import type { ReactNode } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { PixelStuds } from "./pixel-studs";

/**
 * LRM-783 / LRM-784 lock — brand-hero façade for the research home.
 * Pixel theme 2026-08-22: the hero is a `.px-frame` panel with a dithered
 * header strip ("新调研 / NEW RUN"); the old icon+headline block is replaced
 * by the frame header, and `hero_desc` becomes the muted intro line.
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
      className={cn("px-frame relative w-full", className)}
      data-testid="research-home-hero"
      aria-label={t(($) => $.home.composer_label)}
    >
      <PixelStuds />
      <header className="px-frame-header">
        <h2 className="px-frame-title text-sm font-normal text-foreground">
          {t(($) => $.home.frame_title)}
        </h2>
        <small className="hidden shrink-0 text-xs tracking-[0.2em] text-muted-foreground md:block">
          {t(($) => $.home.frame_tag)}
        </small>
      </header>
      <div className="research-home-launch grid lg:grid-cols-[minmax(0,1.15fr)_minmax(380px,0.85fr)]">
        <div className="relative flex min-w-0 flex-col gap-3 p-4 md:p-5">
          <p className="hidden text-xs leading-relaxed text-muted-foreground md:block">
            {t(($) => $.home.hero_desc)}
          </p>
          {children}
        </div>
        {preview}
      </div>
    </section>
  );
}
