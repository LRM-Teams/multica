// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { HonorBadgeCatalogItem } from "@multica/core/types/honor";
import {
  filterHonorBadges,
  getHonorShowcaseBadges,
  getNextHonorBadges,
  honorBadgePresentation,
  honorLevelProgress,
  honorProgressPercent,
  isRareHonorBadge,
} from "./honor-progress";

function badge(
  overrides: Partial<HonorBadgeCatalogItem> & Pick<HonorBadgeCatalogItem, "id">,
): HonorBadgeCatalogItem {
  return {
    title: overrides.id,
    description: `${overrides.id} description`,
    svg_key: "stardust",
    rarity: 10,
    unlock_rule: `Unlock ${overrides.id}`,
    secret: false,
    unlocked: false,
    ...overrides,
  };
}

describe("honor progression presentation", () => {
  it("orders the next attainable badges by closest progress and hides locked secrets", () => {
    const items = [
      badge({ id: "half", progress: { current: 5, target: 10, label: "5 / 10" } }),
      badge({ id: "secret", secret: true, progress: { current: 9, target: 10, label: "9 / 10" } }),
      badge({ id: "near", progress: { current: 8, target: 10, label: "8 / 10" } }),
      badge({ id: "unlocked", unlocked: true }),
      badge({ id: "far", progress: { current: 1, target: 10, label: "1 / 10" } }),
      badge({ id: "unknown" }),
    ];

    expect(getNextHonorBadges(items, 3).map((item) => item.id)).toEqual([
      "near",
      "half",
      "far",
    ]);
  });

  it("fills an empty showcase with the rarest unlocked badges", () => {
    const items = [
      badge({ id: "common", unlocked: true, rarity: 10 }),
      badge({ id: "locked", rarity: 100 }),
      badge({ id: "epic", unlocked: true, rarity: 80 }),
      badge({ id: "rare", unlocked: true, rarity: 40 }),
      badge({ id: "legendary", unlocked: true, rarity: 100 }),
    ];

    expect(getHonorShowcaseBadges(items, []).map((item) => item.id)).toEqual([
      "legendary",
      "epic",
      "rare",
    ]);
  });

  it("preserves explicit showcase order and ignores locked selections", () => {
    const items = [
      badge({ id: "first", unlocked: true, rarity: 10 }),
      badge({ id: "second", unlocked: true, rarity: 100 }),
      badge({ id: "locked", rarity: 200 }),
    ];

    expect(
      getHonorShowcaseBadges(items, ["first", "locked", "second"]).map(
        (item) => item.id,
      ),
    ).toEqual(["first", "second"]);
    expect(
      getHonorShowcaseBadges(items, ["locked"]).map((item) => item.id),
    ).toEqual(["second", "first"]);
  });

  it("redacts every identifying field for a locked secret badge", () => {
    const item = badge({
      id: "hidden",
      title: "Internal title",
      description: "Internal description",
      unlock_rule: "Internal unlock rule",
      svg_key: "quasar",
      secret: true,
    });

    expect(
      honorBadgePresentation(item, {
        secretTitle: "Secret achievement",
        secretDescription: "Keep building to discover it.",
      }),
    ).toEqual({
      title: "Secret achievement",
      description: "Keep building to discover it.",
      svgKey: "stardust",
      redacted: true,
    });
  });

  it("clamps progress and treats an unlock rate of at most nine percent as rare", () => {
    expect(
      honorProgressPercent(
        badge({ id: "overflow", progress: { current: 13, target: 10, label: "13 / 10" } }),
      ),
    ).toBe(100);
    expect(isRareHonorBadge(badge({ id: "rare", unlock_pct: 9 }))).toBe(true);
    expect(isRareHonorBadge(badge({ id: "ordinary", unlock_pct: 9.1 }))).toBe(false);
  });

  it("filters by unlocked, locked, and rare states without mutating catalog order", () => {
    const items = [
      badge({ id: "ordinary", unlocked: true, unlock_pct: 42 }),
      badge({ id: "rare", unlocked: true, unlock_pct: 4.2 }),
      badge({ id: "locked" }),
    ];

    expect(filterHonorBadges(items, "unlocked").map((item) => item.id)).toEqual([
      "ordinary",
      "rare",
    ]);
    expect(filterHonorBadges(items, "locked").map((item) => item.id)).toEqual(["locked"]);
    expect(filterHonorBadges(items, "rare").map((item) => item.id)).toEqual(["rare"]);
    expect(items.map((item) => item.id)).toEqual(["ordinary", "rare", "locked"]);
  });

  it("measures level progress inside the current level instead of against lifetime XP", () => {
    const thresholds = [
      { level: 4, total_xp: 30 },
      { level: 5, total_xp: 50 },
      { level: 6, total_xp: 80 },
    ];

    expect(honorLevelProgress(65, 5, thresholds, 15)).toBe(50);
    expect(honorLevelProgress(80, 6, thresholds, 0)).toBe(100);
  });
});
