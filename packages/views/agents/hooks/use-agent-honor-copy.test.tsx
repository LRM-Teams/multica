// @vitest-environment jsdom

import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import zhAgents from "../../locales/zh-Hans/agents.json";
import { useAgentHonorCopy } from "./use-agent-honor-copy";

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (bundle: typeof zhAgents) => unknown) =>
      String(selector(zhAgents) ?? ""),
  }),
}));

describe("useAgentHonorCopy", () => {
  it("localizes built-in metrics and audit actions", () => {
    const { result } = renderHook(() => useAgentHonorCopy());

    expect(
      [
        "completed",
        "success_streak",
        "memory_writes",
        "evolution_promotions",
        "distinct_projects",
        "recoveries",
        "fleet_class",
      ].map(result.current.metricName),
    ).toEqual([
      "已完成任务",
      "连续成功",
      "有效记忆更新",
      "演化单元晋升",
      "已交付项目",
      "失败后挽回",
      "舰队等级",
    ]);
    expect(
      [
        "rules.update",
        "xp.grant",
        "achievement.grant",
        "achievement.revoke",
      ].map(result.current.auditActionName),
    ).toEqual(["更新荣誉规则", "授予经验", "授予成就", "撤销成就"]);
  });

  it("localizes system event reasons and preserves operator-entered reasons", () => {
    const { result } = renderHook(() => useAgentHonorCopy());

    expect(
      result.current.eventReason({
        event_type: "delivery",
        source_ref: "task-1",
        reason: "Accepted delivery",
      }),
    ).toBe("交付通过验收");
    expect(
      result.current.eventReason({
        event_type: "achievement",
        source_ref: "phoenix_protocol",
        reason: "Phoenix Protocol",
      }),
    ).toBe("凤凰协议");
    expect(
      result.current.eventReason({
        event_type: "manual",
        source_ref: "grant-1",
        reason: "人工补偿误差",
      }),
    ).toBe("人工补偿误差");
  });

  it("preserves unknown protocol values for forward compatibility", () => {
    const { result } = renderHook(() => useAgentHonorCopy());

    expect(result.current.metricName("future_metric")).toBe("future_metric");
    expect(result.current.auditActionName("future.action")).toBe("future.action");
  });
});
