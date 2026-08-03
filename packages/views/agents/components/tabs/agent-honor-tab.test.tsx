// @vitest-environment jsdom

import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import enAgents from "../../../locales/en/agents.json";
import { AchievementCard } from "./agent-honor-tab";

vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (selector: (bundle: typeof enAgents) => unknown) =>
      String(selector(enAgents) ?? ""),
  }),
  useTimeAgo: () => () => "just now",
}));

describe("AchievementCard", () => {
  it("renders an achievement with the warship assigned to its rarity tier", () => {
    const { container } = render(
      <AchievementCard
        achievement={{
          id: "first_launch",
          title: "First Launch",
          description: "Complete the first accepted task.",
          svg_key: "agent_armor_first_launch",
          category: "delivery",
          xp_reward: 25,
          rarity: 10,
          secret: false,
          unlocked: true,
          progress: { current: 1, target: 1 },
        }}
        selected={false}
        equipped={false}
        editable={false}
        onToggle={() => undefined}
        onEquip={() => undefined}
      />,
    );

    expect(container.querySelector('[data-agent-honor-level="1"]')).toBeInTheDocument();
    expect(container.querySelector("svg[aria-label='First Launch']")).not.toBeInTheDocument();
  });
});
