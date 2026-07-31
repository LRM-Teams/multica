"use client";

import { useState } from "react";
import type { HonorBadgeCatalogItem } from "@multica/core/types/honor";
import { HonorBadgeCrest } from "@multica/ui/components/honor/honor-badge";
import { Button } from "@multica/ui/components/ui/button";
import { Progress } from "@multica/ui/components/ui/progress";
import { cn } from "@multica/ui/lib/utils";
import { Check, Gem, LockKeyhole } from "lucide-react";
import {
  filterHonorBadges,
  honorBadgePresentation,
  honorProgressPercent,
  isRareHonorBadge,
  type HonorBadgeFilter,
} from "./honor-progress";

const EMPTY_SHOWCASE_BADGE_IDS: string[] = [];

export interface HonorBadgeCatalogProps {
  items: HonorBadgeCatalogItem[];
  equippedBadgeId?: string | null;
  showcaseBadgeIds?: string[];
  completionLabel: string;
  equipLabel: string;
  equippedLabel: string;
  showcaseLabel: string;
  showcasedLabel: string;
  secretLabel: string;
  secretDescription: string;
  lockedLabel: string;
  rareLabel: string;
  emptyLabel: string;
  filterLabels: Record<HonorBadgeFilter, string>;
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
  showcaseBadgeIds = EMPTY_SHOWCASE_BADGE_IDS,
  completionLabel,
  equipLabel,
  equippedLabel,
  showcaseLabel,
  showcasedLabel,
  secretLabel,
  secretDescription,
  lockedLabel,
  rareLabel,
  emptyLabel,
  filterLabels,
  rarityLabel,
  onEquip,
  onToggleShowcase,
  equipPending,
  showcasePending,
  editable = false,
}: HonorBadgeCatalogProps) {
  const [filter, setFilter] = useState<HonorBadgeFilter>("all");
  const unlocked = items.filter((item) => item.unlocked).length;
  const total = items.length;
  const completionPct = total > 0 ? Math.round((unlocked / total) * 100) : 0;
  const visibleItems = filterHonorBadges(items, filter);
  const filters = Object.keys(filterLabels) as HonorBadgeFilter[];

  return (
    <div className="space-y-5">
      <div className="rounded-xl border border-border/70 bg-muted/25 p-3">
        <div className="mb-2 flex items-center justify-between gap-2">
          <p className="text-sm font-medium tabular-nums">{completionLabel}</p>
          <span className="font-mono text-xs text-muted-foreground">
            {completionPct}%
          </span>
        </div>
        <Progress
          aria-label={completionLabel}
          value={completionPct}
          className="[&_[data-slot=progress-indicator]]:bg-gradient-to-r [&_[data-slot=progress-indicator]]:from-cyan-500 [&_[data-slot=progress-indicator]]:to-violet-500 [&_[data-slot=progress-track]]:h-1.5"
        />
      </div>

      <div className="flex flex-wrap gap-2" aria-label={completionLabel}>
        {filters.map((filterValue) => (
          <button
            key={filterValue}
            type="button"
            aria-pressed={filter === filterValue}
            onClick={() => setFilter(filterValue)}
            className={cn(
              "min-h-8 rounded-full border px-3 text-xs font-medium outline-none transition-colors",
              "focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
              filter === filterValue
                ? "border-foreground/15 bg-foreground text-background"
                : "border-border bg-background text-muted-foreground hover:bg-muted hover:text-foreground",
            )}
          >
            {filterLabels[filterValue]}
          </button>
        ))}
      </div>

      {visibleItems.length === 0 ? (
        <div className="rounded-xl border border-dashed border-border py-10 text-center text-sm text-muted-foreground">
          {emptyLabel}
        </div>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          {visibleItems.map((item) => {
            const presentation = honorBadgePresentation(item, {
              secretTitle: secretLabel,
              secretDescription,
            });
            const equipped = equippedBadgeId === item.id;
            const showcased = showcaseBadgeIds.includes(item.id);
            const progressPct = honorProgressPercent(item);
            const rare = isRareHonorBadge(item);

            return (
              <article
                key={item.id}
                className={cn(
                  "group relative overflow-hidden rounded-2xl border p-4 transition-[border-color,box-shadow,transform]",
                  "motion-safe:hover:-translate-y-0.5",
                  item.unlocked
                    ? "border-border/80 bg-card shadow-sm hover:border-cyan-500/35 hover:shadow-[0_14px_40px_-28px_rgba(34,211,238,0.8)]"
                    : "border-border/60 bg-muted/20",
                  equipped && "border-cyan-500/45 ring-1 ring-cyan-500/20",
                  rare && item.unlocked && "border-amber-500/30",
                )}
              >
                <div
                  aria-hidden="true"
                  className="absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-white/25 to-transparent opacity-0 transition-opacity group-hover:opacity-100"
                />
                <div className="flex items-start gap-4">
                  <HonorBadgeCrest
                    svgKey={presentation.svgKey}
                    title={presentation.title}
                    locked={!item.unlocked}
                    rare={rare && item.unlocked}
                    animated={equipped}
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <h4 className="truncate text-sm font-semibold">
                        {presentation.title}
                      </h4>
                      {presentation.redacted ? (
                        <span className="inline-flex items-center gap-1 rounded-full border border-border bg-muted px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
                          <LockKeyhole className="size-3" aria-hidden="true" />
                          {lockedLabel}
                        </span>
                      ) : null}
                      {rare ? (
                        <span className="inline-flex items-center gap-1 rounded-full border border-amber-500/25 bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-700 dark:text-amber-300">
                          <Gem className="size-3" aria-hidden="true" />
                          {rareLabel}
                        </span>
                      ) : null}
                    </div>
                    <p className="mt-1 line-clamp-2 text-xs leading-5 text-muted-foreground">
                      {presentation.description}
                    </p>
                    {!presentation.redacted &&
                    item.unlock_pct != null &&
                    item.unlock_pct > 0 ? (
                      <p className="mt-2 font-mono text-[11px] text-muted-foreground">
                        {rarityLabel(item.unlock_pct)}
                      </p>
                    ) : null}
                  </div>
                </div>

                {!item.unlocked && item.progress && !presentation.redacted ? (
                  <div className="mt-4 space-y-1.5">
                    <Progress
                      aria-label={item.progress.label}
                      value={progressPct}
                      className="[&_[data-slot=progress-indicator]]:bg-gradient-to-r [&_[data-slot=progress-indicator]]:from-cyan-500 [&_[data-slot=progress-indicator]]:to-violet-500 [&_[data-slot=progress-track]]:h-1.5"
                    />
                    <div className="flex justify-between gap-2 text-[11px] text-muted-foreground">
                      <span className="truncate">{item.progress.label}</span>
                      <span className="shrink-0 font-mono tabular-nums">
                        {item.progress.current}/{item.progress.target}
                      </span>
                    </div>
                  </div>
                ) : null}

                {editable && item.unlocked ? (
                  <div className="mt-4 flex flex-wrap gap-2">
                    <Button
                      type="button"
                      size="xs"
                      variant={equipped ? "default" : "outline"}
                      aria-pressed={equipped}
                      disabled={equipPending}
                      onClick={() => onEquip?.(item.id)}
                    >
                      {equipped ? (
                        <Check className="size-3" aria-hidden="true" />
                      ) : null}
                      {equipped ? equippedLabel : equipLabel}
                    </Button>
                    <Button
                      type="button"
                      size="xs"
                      variant={showcased ? "secondary" : "outline"}
                      aria-pressed={showcased}
                      disabled={showcasePending}
                      onClick={() => onToggleShowcase?.(item.id)}
                    >
                      {showcased ? showcasedLabel : showcaseLabel}
                    </Button>
                  </div>
                ) : null}
              </article>
            );
          })}
        </div>
      )}
    </div>
  );
}
