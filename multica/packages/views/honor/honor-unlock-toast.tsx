"use client";

import type { ReactNode } from "react";
import { Gem, Sparkles, X } from "lucide-react";
import { HonorBadgeIcon } from "@multica/ui/components/honor/honor-badge";
import { cn } from "@multica/ui/lib/utils";

export const honorUnlockToastOptions = {
  duration: 6000,
  position: "top-right" as const,
  unstyled: true,
};

export function HonorUnlockToast({
  eyebrow,
  title,
  meta,
  svgKey,
  rare = false,
  dismissLabel,
  onDismiss,
}: {
  eyebrow: string;
  title: string;
  meta?: ReactNode;
  svgKey: string;
  rare?: boolean;
  dismissLabel: string;
  onDismiss: () => void;
}) {
  const EyebrowIcon = rare ? Gem : Sparkles;

  return (
    <output
      data-testid="honor-unlock-toast"
      className="relative flex w-[min(340px,calc(100vw-1.5rem))] items-center gap-3 overflow-hidden rounded-xl border border-border/80 bg-popover/95 p-3 pr-10 text-popover-foreground shadow-[0_16px_48px_-20px_rgba(15,23,42,0.45)] backdrop-blur-xl"
    >
      <span
        className={cn(
          "grid size-11 shrink-0 place-items-center rounded-xl border border-border/70 bg-muted/65",
          rare &&
            "border-amber-400/45 bg-amber-500/10 shadow-[0_0_18px_-8px_rgba(245,158,11,0.75)]",
        )}
      >
        <HonorBadgeIcon
          svgKey={svgKey}
          title={title}
          medal
          className="size-9"
        />
      </span>

      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-[0.08em] text-muted-foreground">
          <EyebrowIcon
            className={cn("size-3 text-cyan-600 dark:text-cyan-300", rare && "text-amber-600 dark:text-amber-300")}
            aria-hidden="true"
          />
          {eyebrow}
        </span>
        <span className="mt-0.5 block truncate text-sm font-semibold leading-5">
          {title}
        </span>
        {meta ? (
          <span className="mt-0.5 block truncate text-[11px] leading-4 text-muted-foreground">
            {meta}
          </span>
        ) : null}
      </span>

      <button
        type="button"
        aria-label={dismissLabel}
        onClick={onDismiss}
        className="absolute right-2 top-2 grid size-6 place-items-center rounded-md text-muted-foreground outline-none transition-colors hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
      >
        <X className="size-3.5" aria-hidden="true" />
      </button>
    </output>
  );
}
