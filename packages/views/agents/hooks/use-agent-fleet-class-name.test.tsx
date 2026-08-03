// @vitest-environment jsdom

import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import zhAgents from "../../locales/zh-Hans/agents.json";
import { useAgentFleetClassName } from "./use-agent-fleet-class-name";

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (bundle: typeof zhAgents) => unknown) =>
      String(selector(zhAgents) ?? ""),
  }),
}));

describe("useAgentFleetClassName", () => {
  it("localizes every built-in fleet class into Chinese", () => {
    const { result } = renderHook(() => useAgentFleetClassName());

    expect([
      result.current("reserve"),
      result.current("corvette"),
      result.current("frigate"),
      result.current("cruiser"),
      result.current("battleship"),
      result.current("dreadnought"),
    ]).toEqual([
      "预备舰",
      "轻型护卫舰",
      "护卫舰",
      "巡洋舰",
      "战列舰",
      "无畏舰",
    ]);
  });

  it("preserves a server label for an unknown future class", () => {
    const { result } = renderHook(() => useAgentFleetClassName());

    expect(result.current("carrier", "Carrier")).toBe("Carrier");
  });
});
