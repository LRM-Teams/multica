import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { HonorBadgeCatalogItem } from "@multica/core/types/honor";
import { HonorBadgeCatalog } from "./honor-badge-catalog";

const unlockedBadge: HonorBadgeCatalogItem = {
  id: "shipper",
  title: "Shipper",
  description: "Delivered an Issue",
  svg_key: "comet",
  rarity: 2,
  unlock_rule: "Close one Issue",
  secret: false,
  unlocked: true,
  unlock_pct: 7.2,
};

const secretBadge: HonorBadgeCatalogItem = {
  id: "classified",
  title: "Internal classified title",
  description: "Internal classified description",
  svg_key: "genesis-nebula",
  rarity: 4,
  unlock_rule: "Internal classified unlock rule",
  secret: true,
  unlocked: false,
  unlock_pct: 0.5,
  progress: {
    current: 9,
    target: 10,
    label: "Internal classified progress",
  },
};

const labels = {
  completionLabel: "1 / 2 badges",
  equipLabel: "Equip",
  equippedLabel: "Equipped",
  showcaseLabel: "Showcase",
  showcasedLabel: "Showcased",
  secretLabel: "Secret achievement",
  secretDescription: "Keep building to reveal this achievement.",
  lockedLabel: "Locked",
  rareLabel: "Rare",
  emptyLabel: "No achievements",
  filterLabels: {
    all: "All",
    unlocked: "Unlocked",
    locked: "Locked",
    rare: "Rare",
  },
  rarityLabel: (pct: number) => `${pct}%`,
};

describe("HonorBadgeCatalog", () => {
  it("fully redacts a locked secret achievement", () => {
    render(
      <HonorBadgeCatalog
        {...labels}
        items={[unlockedBadge, secretBadge]}
      />,
    );

    expect(
      screen.getByRole("heading", { name: "Secret achievement" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Keep building to reveal this achievement."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Internal classified title"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("Internal classified description"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("Internal classified unlock rule"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("Internal classified progress"),
    ).not.toBeInTheDocument();
  });

  it("filters the collection and exposes pressed state", async () => {
    const user = userEvent.setup();
    render(
      <HonorBadgeCatalog
        {...labels}
        items={[unlockedBadge, secretBadge]}
      />,
    );

    const unlockedFilter = screen.getByRole("button", { name: "Unlocked" });
    await user.click(unlockedFilter);

    expect(unlockedFilter).toHaveAttribute("aria-pressed", "true");
    expect(
      screen.getByRole("heading", { name: "Shipper" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Secret achievement")).not.toBeInTheDocument();
  });

  it("announces equipped and showcased selection state", async () => {
    const user = userEvent.setup();
    const onEquip = vi.fn();
    const onToggleShowcase = vi.fn();
    render(
      <HonorBadgeCatalog
        {...labels}
        items={[unlockedBadge]}
        editable
        equippedBadgeId="shipper"
        showcaseBadgeIds={["shipper"]}
        onEquip={onEquip}
        onToggleShowcase={onToggleShowcase}
      />,
    );

    const equipButton = screen.getByRole("button", { name: "Equipped" });
    const showcaseButton = screen.getByRole("button", { name: "Showcased" });
    expect(equipButton).toHaveAttribute("aria-pressed", "true");
    expect(showcaseButton).toHaveAttribute("aria-pressed", "true");

    await user.click(equipButton);
    await user.click(showcaseButton);
    expect(onEquip).toHaveBeenCalledWith("shipper");
    expect(onToggleShowcase).toHaveBeenCalledWith("shipper");
  });
});
