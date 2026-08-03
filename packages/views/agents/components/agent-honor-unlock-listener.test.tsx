// @vitest-environment jsdom

import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import zhAgents from "../../locales/zh-Hans/agents.json";
import { AgentHonorUnlockListener } from "./agent-honor-unlock-listener";

const mocks = vi.hoisted(() => ({
  eventHandlers: new Map<string, (payload: unknown) => void>(),
  invalidateQueries: vi.fn(),
  getQueryData: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({
    invalidateQueries: mocks.invalidateQueries,
    getQueryData: mocks.getQueryData,
  }),
}));

vi.mock("@multica/core/agents", () => ({
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
    custom: vi.fn(),
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
    mocks.toastSuccess.mockReset();
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
});
