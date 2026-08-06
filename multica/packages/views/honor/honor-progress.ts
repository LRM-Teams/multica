import type { HonorBadgeCatalogItem } from "@multica/core/types/honor";

export type HonorBadgeFilter = "all" | "unlocked" | "locked" | "rare";

export interface HonorBadgePresentation {
  title: string;
  description: string;
  svgKey: string;
  redacted: boolean;
}

const rareUnlockPercentage = 9;

export function honorProgressPercent(item: HonorBadgeCatalogItem): number {
  if (item.unlocked) return 100;
  if (!item.progress || item.progress.target <= 0) return 0;
  return Math.max(
    0,
    Math.min(100, Math.round((item.progress.current / item.progress.target) * 100)),
  );
}

export function honorLevelProgress(
  totalXp: number,
  level: number,
  thresholds: Array<{ level: number; total_xp: number }>,
  xpToNextLevel: number,
): number {
  if (xpToNextLevel <= 0) return 100;

  const currentLevelXp =
    thresholds.find((threshold) => threshold.level === level)?.total_xp;
  const nextLevelXp =
    thresholds.find((threshold) => threshold.level === level + 1)?.total_xp;

  if (
    currentLevelXp != null &&
    nextLevelXp != null &&
    nextLevelXp > currentLevelXp
  ) {
    return Math.max(
      0,
      Math.min(
        100,
        Math.round(
          ((totalXp - currentLevelXp) / (nextLevelXp - currentLevelXp)) * 100,
        ),
      ),
    );
  }

  return Math.max(
    0,
    Math.min(100, Math.round((100 * totalXp) / (totalXp + xpToNextLevel))),
  );
}

export function isRareHonorBadge(item: HonorBadgeCatalogItem): boolean {
  return (
    item.unlock_pct != null &&
    item.unlock_pct > 0 &&
    item.unlock_pct <= rareUnlockPercentage
  );
}

export function honorBadgePresentation(
  item: HonorBadgeCatalogItem,
  labels: {
    secretTitle: string;
    secretDescription: string;
  },
): HonorBadgePresentation {
  if (item.secret && !item.unlocked) {
    return {
      title: labels.secretTitle,
      description: labels.secretDescription,
      svgKey: "stardust",
      redacted: true,
    };
  }

  return {
    title: item.title,
    description: item.unlocked
      ? item.description
      : item.unlock_rule || item.description,
    svgKey: item.svg_key,
    redacted: false,
  };
}

export function getNextHonorBadges(
  items: HonorBadgeCatalogItem[],
  limit = 3,
): HonorBadgeCatalogItem[] {
  return [...items]
    .filter(
      (item) =>
        !item.unlocked &&
        !item.secret &&
        item.progress != null &&
        item.progress.target > 0,
    )
    .sort((left, right) => {
      const progressDelta = honorProgressPercent(right) - honorProgressPercent(left);
      if (progressDelta !== 0) return progressDelta;

      const leftRemaining = left.progress!.target - left.progress!.current;
      const rightRemaining = right.progress!.target - right.progress!.current;
      if (leftRemaining !== rightRemaining) return leftRemaining - rightRemaining;

      return right.rarity - left.rarity;
    })
    .slice(0, limit);
}

/**
 * Resolve the public showcase without leaving a new user's cabinet empty.
 * Explicit, unlocked selections keep their saved order. When no saved
 * selection is usable, the rarest unlocked badges become the default set.
 */
export function getHonorShowcaseBadges(
  items: HonorBadgeCatalogItem[],
  showcaseIds: string[],
  limit = 3,
): HonorBadgeCatalogItem[] {
  const catalogById = new Map(items.map((item) => [item.id, item]));
  const selected = showcaseIds
    .map((id) => catalogById.get(id))
    .filter(
      (item): item is HonorBadgeCatalogItem => item?.unlocked === true,
    )
    .slice(0, limit);

  if (selected.length > 0) return selected;

  return [...items]
    .filter((item) => item.unlocked)
    .sort(
      (left, right) =>
        right.rarity - left.rarity || left.id.localeCompare(right.id),
    )
    .slice(0, limit);
}

export function filterHonorBadges(
  items: HonorBadgeCatalogItem[],
  filter: HonorBadgeFilter,
): HonorBadgeCatalogItem[] {
  switch (filter) {
    case "unlocked":
      return items.filter((item) => item.unlocked);
    case "locked":
      return items.filter((item) => !item.unlocked);
    case "rare":
      return items.filter(isRareHonorBadge);
    default:
      return [...items];
  }
}
