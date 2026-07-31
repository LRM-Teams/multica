"use client";

import type { HonorBadgeCatalogItem } from "@multica/core/types/honor";
import { HonorBadgeIcon } from "@multica/ui/components/honor/honor-badge";
import { Button } from "@multica/ui/components/ui/button";
import { Progress } from "@multica/ui/components/ui/progress";
import { cn } from "@multica/ui/lib/utils";

export interface HonorBadgeCatalogProps {
  items: HonorBadgeCatalogItem[];
  equippedBadgeId?: string | null;
  showcaseBadgeIds?: string[];
  completionLabel: string;
  equipLabel: string;
  showcaseLabel: string;
  secretLabel: string;
  lockedLabel: string;
  rarityLabel: (pct: number) => string;
  onEquip?: (badgeId: string) => void;
  onToggleShowcase?: (badgeId: string) => void;
  equipPending?: boolean;
  showcasePending?: boolean;
  editable?: boolean;
}

export function HonorBadgeCatalog({
  items,
  equippedBadgeId,
  showcaseBadgeIds = [],
  completionLabel,
  equipLabel,
  showcaseLabel,
  secretLabel,
  lockedLabel,
  rarityLabel,
  onEquip,
  onToggleShowcase,
  equipPending,
  showcasePending,
  editable = false,
}: HonorBadgeCatalogProps) {
  const unlocked = items.filter((item) => item.unlocked).length;
  const total = items.length;
  const completionPct = total > 0 ? Math.round((unlocked / total) * 100) : 0;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm font-medium tabular-nums">{completionLabel}</p>
        <span className="text-xs text-muted-foreground">{completionPct}%</span>
      </div>
      <Progress value={completionPct} />
      <div className="grid gap-3 sm:grid-cols-2">
        {items.map((item) => {
          const equipped = equippedBadgeId === item.id;
          const showcased = showcaseBadgeIds.includes(item.id);
          const progressPct =
            item.progress && item.progress.target > 0
              ? Math.min(100, Math.round((item.progress.current / item.progress.target) * 100))
              : item.unlocked
                ? 100
                : 0;
          return (
            <div
              key={item.id}
              className={cn(
                "rounded-lg border p-3 transition-colors",
                item.unlocked ? "border-border bg-card" : "border-border/70 bg-muted/20 opacity-90",
                equipped && "ring-2 ring-primary/40",
              )}
            >
              <div className="flex items-start gap-3">
                <div className={cn(!item.unlocked && "grayscale opacity-60")}>
                  <HonorBadgeIcon svgKey={item.svg_key} title={item.title} medal />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <p className="truncate text-sm font-semibold">{item.title}</p>
                    {item.secret && !item.unlocked ? (
                      <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
                        {secretLabel}
                      </span>
                    ) : null}
                  </div>
                  <p className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">
                    {item.unlocked ? item.description : item.unlock_rule || lockedLabel}
                  </p>
                  {item.unlock_pct != null && item.unlock_pct > 0 ? (
                    <p className="mt-1 text-[11px] text-muted-foreground">{rarityLabel(item.unlock_pct)}</p>
                  ) : null}
                  {!item.unlocked && item.progress ? (
                    <div className="mt-2 space-y-1">
                      <Progress value={progressPct} className="h-1.5" />
                      <p className="text-[11px] tabular-nums text-muted-foreground">
                        {item.progress.current}/{item.progress.target}
                      </p>
                    </div>
                  ) : null}
                  {editable && item.unlocked ? (
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      <Button
                        type="button"
                        size="xs"
                        variant={equipped ? "default" : "outline"}
                        disabled={equipPending}
                        onClick={() => onEquip?.(item.id)}
                      >
                        {equipLabel}
                      </Button>
                      <Button
                        type="button"
                        size="xs"
                        variant={showcased ? "secondary" : "outline"}
                        disabled={showcasePending}
                        onClick={() => onToggleShowcase?.(item.id)}
                      >
                        {showcaseLabel}
                      </Button>
                    </div>
                  ) : null}
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
