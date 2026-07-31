"use client";

import type { HonorCompareResult, HonorPublicWall } from "@multica/core/types/honor";
import { HonorBadgeCrest, HonorBadgeIcon } from "@multica/ui/components/honor/honor-badge";
import { Progress } from "@multica/ui/components/ui/progress";

export interface HonorWallProps {
  wall: HonorPublicWall;
  completionLabel: string;
  statsLabel: string;
  showcaseTitle: string;
  recentTitle: string;
  compare?: HonorCompareResult | null;
  compareTitle?: string;
  sharedTitle?: string;
  youOnlyTitle?: string;
  themOnlyTitle?: string;
}

export function HonorWall({
  wall,
  completionLabel,
  statsLabel,
  showcaseTitle,
  recentTitle,
  compare,
  compareTitle,
  sharedTitle,
  youOnlyTitle,
  themOnlyTitle,
}: HonorWallProps) {
  const unlocked = wall.badges_unlocked ?? wall.unlocked_badges.length;
  const total = wall.badges_total ?? wall.unlocked_badges.length;
  const pct = total > 0 ? Math.round((unlocked / total) * 100) : 0;

  return (
    <div className="space-y-5">
      <div className="rounded-xl border border-border/70 bg-muted/20 p-3">
        <div className="mb-1 flex items-center justify-between text-xs text-muted-foreground">
          <span>{completionLabel}</span>
          <span className="font-mono tabular-nums">{statsLabel}</span>
        </div>
        <Progress
          aria-label={completionLabel}
          value={pct}
          className="[&_[data-slot=progress-indicator]]:bg-gradient-to-r [&_[data-slot=progress-indicator]]:from-cyan-500 [&_[data-slot=progress-indicator]]:to-violet-500 [&_[data-slot=progress-track]]:h-1.5"
        />
      </div>

      {wall.showcase_badges && wall.showcase_badges.length > 0 ? (
        <section className="overflow-hidden rounded-2xl border border-violet-500/20 bg-[linear-gradient(145deg,rgba(15,23,42,1),rgba(30,27,75,0.94))] p-4 text-white">
          <h4 className="mb-3 text-[10px] font-semibold uppercase tracking-[0.14em] text-violet-200">
            {showcaseTitle}
          </h4>
          <div className="grid grid-cols-3 gap-2">
            {wall.showcase_badges.map((badge) => (
              <div
                key={badge.id}
                className="flex min-h-28 flex-col items-center justify-center rounded-xl border border-white/10 bg-white/5 p-2 text-center"
              >
                <HonorBadgeCrest
                  svgKey={badge.svg_key}
                  title={badge.title}
                  animated
                  className="size-12"
                />
                <span className="mt-2 line-clamp-2 text-[10px] font-medium leading-4 text-slate-200">
                  {badge.title}
                </span>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      {wall.recent_unlocks && wall.recent_unlocks.length > 0 ? (
        <div>
          <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-[0.05em] text-muted-foreground">
            {recentTitle}
          </h4>
          <ul className="space-y-1">
            {wall.recent_unlocks.map((item) => (
              <li
                key={`${item.id}-${item.unlocked_at}`}
                className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-xs hover:bg-muted/50"
              >
                <HonorBadgeIcon svgKey={item.svg_key} title={item.title} medal />
                <span className="font-medium">{item.title}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {compare && compareTitle ? (
        <div className="space-y-2 border-t border-border pt-3">
          <h4 className="text-[11px] font-semibold uppercase tracking-[0.05em] text-muted-foreground">
            {compareTitle}
          </h4>
          <CompareBadgeGroup title={sharedTitle ?? ""} badges={compare.shared_badges} />
          <CompareBadgeGroup title={youOnlyTitle ?? ""} badges={compare.self_only_badges} />
          <CompareBadgeGroup title={themOnlyTitle ?? ""} badges={compare.other_only_badges} />
        </div>
      ) : null}
    </div>
  );
}

function CompareBadgeGroup({
  title,
  badges,
}: {
  title: string;
  badges: HonorCompareResult["shared_badges"];
}) {
  if (badges.length === 0) return null;
  return (
    <div>
      <p className="mb-1 text-[11px] text-muted-foreground">{title}</p>
      <div className="flex flex-wrap gap-1">
        {badges.map((badge) => (
          <HonorBadgeIcon key={badge.id} svgKey={badge.svg_key} title={badge.title} />
        ))}
      </div>
    </div>
  );
}
