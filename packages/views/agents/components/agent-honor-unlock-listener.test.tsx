// @vitest-environment jsdom

import { act, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import zhAgents from "../../locales/zh-Hans/agents.json";
import { AgentHonorUnlockListener } from "./agent-honor-unlock-listener";

const mocks = vi.hoisted(() => ({
  eventHandlers: new Map<string, (payload: unknown) => void>(),
  invalidateQueries: vi.fn(),
  getQueryData: vi.fn(),
  ensureQueryData: vi.fn(),
  toastSuccess: vi.fn(),
  toastCustom: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({
    invalidateQueries: mocks.invalidateQueries,
    getQueryData: mocks.getQueryData,
    ensureQueryData: mocks.ensureQueryData,
  }),
}));

vi.mock("@multica/core/agents", () => ({
  agentDetailOptions: (workspaceId: string, agentId: string) => ({
    queryKey: ["agents", workspaceId, "detail", agentId],
    queryFn: vi.fn(),
  }),
  agentHonorKeys: {
    dashboard: (workspaceId: string, agentId: string) => [
      "agents",
      workspaceId,
      "honor",
      agentId,
    ],
  },
}));

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => "workspace-1",
}));

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: (event: string, handler: (payload: unknown) => void) => {
    mocks.eventHandlers.set(event, handler);
  },
}));

vi.mock("@multica/core/workspace/queries", () => ({
  workspaceKeys: {
    agents: (workspaceId: string) => ["agents", workspaceId],
  },
}));

vi.mock("sonner", () => ({
  toast: {
    success: mocks.toastSuccess,
    custom: mocks.toastCustom,
    dismiss: vi.fn(),
  },
}));

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

describe("AgentHonorUnlockListener", () => {
  beforeEach(() => {
    mocks.eventHandlers.clear();
    mocks.invalidateQueries.mockReset();
    mocks.getQueryData.mockReset();
    mocks.ensureQueryData.mockReset();
    mocks.toastSuccess.mockReset();
    mocks.toastCustom.mockReset();
  });

  it("shows achievement experience in Chinese", () => {
    render(<AgentHonorUnlockListener />);

    act(() => {
      mocks.eventHandlers.get("agent_honor:achievement_unlocked")?.({
        agent_id: "agent-1",
        achievement: {
          id: "first_launch",
          title: "First Launch",
          description: "Complete the first accepted task.",
          svg_key: "agent_armor_first_launch",
          category: "delivery",
          xp_reward: 25,
          rarity: 10,
          secret: false,
          unlocked: true,
        },
      });
    });

    const renderToast = mocks.toastCustom.mock.calls[0]?.[0];
    expect(renderToast).toBeTypeOf("function");
    render(renderToast("toast-1"));
    expect(screen.getByText("+25 经验")).toBeInTheDocument();
    expect(screen.queryByText(/XP/)).not.toBeInTheDocument();
  });

  it("names the promoted agent and localizes the fleet class", () => {
    render(<AgentHonorUnlockListener />);

    act(() => {
      mocks.eventHandlers.get("agent_honor:fleet_class_changed")?.({
        agent_id: "agent-1",
        agent_name: "前端工程师",
        previous_class_id: "corvette",
        class_id: "frigate",
        fleet_score: 42,
      });
    });

    expect(mocks.toastSuccess).toHaveBeenCalledWith("前端工程师晋升至护卫舰");
  });

  it("uses the cached agent name for events from an older server", () => {
    mocks.getQueryData.mockReturnValue([
      {
        id: "agent-1",
        name: "frontend-engineer",
        display_name: "前端工程师",
      },
    ]);
    render(<AgentHonorUnlockListener />);

    act(() => {
      mocks.eventHandlers.get("agent_honor:fleet_class_changed")?.({
        agent_id: "agent-1",
        previous_class_id: "corvette",
        class_id: "frigate",
        fleet_score: 42,
      });
    });

    expect(mocks.toastSuccess).toHaveBeenCalledWith("前端工程师晋升至护卫舰");
  });

  it("loads the authoritative agent name when an older event arrives before the list cache", async () => {
    mocks.getQueryData.mockReturnValue(undefined);
    mocks.ensureQueryData.mockResolvedValue({
      id: "agent-1",
      name: "frontend-engineer",
      display_name: "前端工程师",
    });
    render(<AgentHonorUnlockListener />);

    act(() => {
      mocks.eventHandlers.get("agent_honor:fleet_class_changed")?.({
        agent_id: "agent-1",
        previous_class_id: "corvette",
        class_id: "frigate",
        fleet_score: 42,
      });
    });

    await waitFor(() => {
      expect(mocks.toastSuccess).toHaveBeenCalledWith("前端工程师晋升至护卫舰");
    });
    expect(mocks.toastSuccess).not.toHaveBeenCalledWith("智能体晋升至护卫舰");
  });

  it("does not emit an anonymous promotion when the agent cannot be resolved", async () => {
    mocks.getQueryData.mockReturnValue(undefined);
    mocks.ensureQueryData.mockRejectedValue(new Error("agent unavailable"));
    render(<AgentHonorUnlockListener />);

    act(() => {
      mocks.eventHandlers.get("agent_honor:fleet_class_changed")?.({
        agent_id: "agent-1",
        previous_class_id: "corvette",
        class_id: "frigate",
        fleet_score: 42,
      });
    });

    await waitFor(() => {
      expect(mocks.ensureQueryData).toHaveBeenCalledTimes(1);
    });
    expect(mocks.toastSuccess).not.toHaveBeenCalled();
  });
});
