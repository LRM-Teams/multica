// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import zhAgents from "../../../locales/zh-Hans/agents.json";
import { AchievementCard, AgentHonorAdminContent } from "./agent-honor-tab";

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@tanstack/react-query", () => ({
  useMutation: () => ({ mutate: vi.fn(), isPending: false }),
  useQuery: vi.fn(),
  useQueryClient: () => ({
    invalidateQueries: vi.fn(),
    setQueryData: vi.fn(),
  }),
}));

vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (
      selector: (bundle: typeof zhAgents) => unknown,
      options?: Record<string, string | number>,
    ) => {
      const template = selector(zhAgents);
      if (typeof template !== "string") return String(template ?? "");
      return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(options?.[key] ?? `{{${key}}}`),
      );
    },
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
    expect(container.querySelector("svg[aria-label='星海首航']")).not.toBeInTheDocument();
    expect(container).toHaveTextContent("+25 经验");
    expect(container).not.toHaveTextContent("XP");
  });
});

describe("AgentHonorAdminContent", () => {
  it("gives every achievement and manual correction control an accessible name", () => {
    render(
      <AgentHonorAdminContent
        agent={{ id: "agent-1" }}
        rulesView={{
          revision: 1,
          rules: {
            version: "test",
            completion_xp: 25,
            fleet_window_days: 30,
            fleet_min_sample_tasks: 3,
            fleet_weights: {
              delivery: 0.25,
              evolution: 0.25,
              growth: 0.25,
              efficiency: 0.25,
            },
            fleet_classes: [],
            achievement_targets: { first_launch: 1 },
            achievement_enabled: { first_launch: true },
            changelog: [],
          },
          achievements: [
            {
              id: "first_launch",
              title: "First Launch",
              description: "Complete the first accepted task.",
              svg_key: "agent_armor_first_launch",
              category: "delivery",
              metric: "completed",
              target: 1,
              xp_reward: 25,
              rarity: 10,
              secret: false,
            },
          ],
        }}
        audit={[]}
      />,
    );

    expect(screen.getByRole("switch", { name: "启用星海首航" })).toBeVisible();
    expect(
      screen.getByRole("spinbutton", { name: "星海首航目标值" }),
    ).toBeVisible();
    const correctionType = screen.getByRole("combobox", {
      name: "修正类型",
    });
    expect(correctionType).toBeVisible();
    expect(
      screen.getByRole("spinbutton", { name: "经验调整值" }),
    ).toBeVisible();
    expect(screen.getByRole("textbox", { name: "修正原因" })).toBeVisible();

    fireEvent.change(correctionType, { target: { value: "achievement" } });
    expect(
      screen.getByRole("combobox", { name: "选择成就" }),
    ).toBeVisible();
  });
});
