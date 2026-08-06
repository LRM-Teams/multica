"use client";

import { cn } from "@multica/ui/lib/utils";

/**
 * LRM-783 / LRM-971 — shared research shell atmosphere:
 * soft brand wash + fading dot-grid (canvas chrome extension).
 */
export function ResearchShellAtmosphere({
  className,
  heightClassName = "h-[280px]",
}: {
  className?: string;
  /** Tailwind height utility for the grid plane. */
  heightClassName?: string;
}) {
  return (
    <div
      aria-hidden
      data-testid="research-shell-atmosphere"
      className={cn(
        "pointer-events-none absolute inset-x-0 top-0 z-0 overflow-hidden",
        heightClassName,
        className,
      )}
    >
      <div className="absolute inset-0 bg-[radial-gradient(120%_80%_at_50%_-10%,color-mix(in_oklab,var(--brand)_10%,transparent),transparent_55%)]" />
      <div
        className="absolute inset-0 opacity-70 [mask-image:linear-gradient(to_bottom,black_30%,transparent)]"
        style={{
          backgroundImage:
            "radial-gradient(circle, color-mix(in oklab, var(--foreground) 11%, transparent) 1px, transparent 1.5px)",
          backgroundSize: "24px 24px",
        }}
      />
    </div>
  );
}
