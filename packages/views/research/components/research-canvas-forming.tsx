"use client";

import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n/use-t";

/**
 * LRM-979 — in-flight zero-node canvas: skeleton cards on the canvas grid
 * (no blank flash / gray pit). Tokens follow canvas-bg / card family (793/972).
 */
export function ResearchCanvasForming() {
  const { t } = useT("research");

  return (
    <div
      data-testid="research-session-canvas-forming"
      className="absolute inset-0 z-[5] flex flex-col items-center justify-center gap-4 bg-canvas-bg/88 px-6 py-8"
      aria-busy="true"
      aria-live="polite"
    >
      <div
        className="relative w-full max-w-lg rounded-xl border border-border/50 p-5"
        style={{
          backgroundImage:
            "radial-gradient(circle, color-mix(in oklab, var(--foreground) 8%, transparent) 1px, transparent 1.5px)",
          backgroundSize: "22px 22px",
        }}
      >
        <div className="flex flex-wrap gap-3">
          <Skeleton className="h-[72px] w-[160px] rounded-xl" />
          <Skeleton className="mt-6 h-[72px] w-[160px] rounded-xl opacity-80" />
          <Skeleton className="h-[72px] w-[140px] rounded-xl opacity-60" />
        </div>
        <div className="mt-4 flex gap-2">
          <Skeleton className="h-2 w-16 rounded-full" />
          <Skeleton className="h-2 w-24 rounded-full" />
        </div>
      </div>
      <div className="max-w-sm text-center">
        <p className="text-sm font-medium text-foreground">
          {t(($) => $.session_page.canvas_forming_title)}
        </p>
        <p className="mt-1.5 text-xs text-muted-foreground">
          {t(($) => $.session_page.canvas_forming_hint)}
        </p>
      </div>
    </div>
  );
}
