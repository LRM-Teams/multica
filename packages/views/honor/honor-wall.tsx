"use client";

import type { HonorCompareResult, HonorPublicWall } from "@multica/core/types/honor";
import { HonorBadgeIcon } from "@multica/ui/components/honor/honor-badge";
import { Progress } from "@multica/ui/components/ui/progress";

export interface HonorWallProps {
  wall: HonorPublicWall;
  completionLabel: string;
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
    <div className="space-y-4">
      <div>
        <div className="mb-1 flex items-center justify-between text-xs text-muted-foreground">
          <span>{completionLabel}</span>
          <span className="tabular-nums">
            {unlocked}/{total} · Lv.{wall.level}
          </span>
        </div>
        <Progress value={pct} />
      </div>

      {wall.showcase_badges && wall.showcase_badges.length > 0 ? (
        <div>
          <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-[0.05em] text-muted-foreground">
            {showcaseTitle}
          </h4>
          <div className="flex flex-wrap gap-2">
            {wall.showcase_badges.map((badge) => (
              <div key={badge.id} className="flex items-center gap-1.5 rounded-md border border-border px-2 py-1">
                <HonorBadgeIcon svgKey={badge.svg_key} title={badge.title} medal />
                <span className="text-xs font-medium">{badge.title}</span>
              </div>
            ))}
          </div>
        </div>
      ) : null}

      {wall.recent_unlocks && wall.recent_unlocks.length > 0 ? (
        <div>
          <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-[0.05em] text-muted-foreground">
            {recentTitle}
          </h4>
          <ul className="space-y-1.5">
            {wall.recent_unlocks.map((item) => (
              <li key={`${item.id}-${item.unlocked_at}`} className="flex items-center gap-2 text-xs">
                <HonorBadgeIcon svgKey={item.svg_key} title={item.title} />
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
