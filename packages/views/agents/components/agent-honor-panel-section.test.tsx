// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import zhAgents from "../../locales/zh-Hans/agents.json";
import { AgentHonorPanelSection } from "./agent-honor-panel-section";

const mockHonor = {
  agent_id: "agent-1",
  level: 8,
  total_xp: 1_250,
  xp_to_next_level: 150,
  equipped_achievement_id: "first_launch",
  showcase_achievement_ids: ["first_launch"],
  metrics: {
    completed_count: 12,
    failed_count: 0,
    success_streak: 5,
    memory_writes: 3,
    evolution_promotions: 0,
    distinct_projects: 2,
    recovery_count: 0,
  },
  fleet: {
    agent_id: "agent-1",
    fleet_score: 55,
    class_id: "corvette",
    class_label: "Corvette",
    fleet_rank: 4,
    fleet_size: 12,
    sample_tasks: 12,
    sample_sufficient: true,
    frozen: false,
    pillars: {
      delivery: 0.7,
      evolution: 0.4,
      growth: 0.5,
      efficiency: 0.6,
    },
  },
  achievements: [
    {
      id: "first_launch",
      title: "First Launch",
      description: "Complete the first task.",
      svg_key: "agent_armor_first_launch",
      category: "delivery",
      xp_reward: 25,
      rarity: 10,
      secret: false,
      unlocked: true,
      unlocked_at: "2026-08-01T10:00:00Z",
    },
    {
      id: "centurion",
      title: "Centurion",
      description: "Complete 100 tasks.",
      svg_key: "agent_armor_centurion",
      category: "delivery",
      xp_reward: 250,
      rarity: 65,
      secret: false,
      unlocked: false,
      progress: { current: 12, target: 100 },
    },
  ],
  recent_events: [],
  fleet_history: [],
  rules_version: "test",
};

vi.mock("../../i18n", () => ({
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
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/acme/agents/${id}`,
  }),
}));

vi.mock("@multica/core/agents", () => ({
  agentHonorOptions: () => ({ queryKey: ["agent-honor"] }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({
    data: mockHonor,
    isPending: false,
    isError: false,
  }),
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    href,
    children,
    ...props
  }: {
    href: string;
    children: React.ReactNode;
  }) => (
    <a href={href} {...props}>
      {children}
    </a>
  ),
}));

describe("AgentHonorPanelSection", () => {
  it("renders the level-specific warship icon and honor summary", () => {
    const { container } = render(
      <AgentHonorPanelSection agentId="agent-1" workspaceId="ws-1" />,
    );

    expect(screen.getByTestId("agent-honor-panel-section")).toBeInTheDocument();
    expect(container.querySelector('[data-agent-honor-level="8"]')).toBeInTheDocument();
    expect(screen.getByText("第 8 级")).toBeInTheDocument();
    expect(screen.getByText(/1250 经验/)).toBeInTheDocument();
    expect(screen.queryByText(/XP|LV\./)).not.toBeInTheDocument();

    const viewAll = screen.getByTestId("agent-honor-view-all");
    expect(viewAll).toHaveAttribute("href", "/acme/agents/agent-1?tab=honor");
  });
});
