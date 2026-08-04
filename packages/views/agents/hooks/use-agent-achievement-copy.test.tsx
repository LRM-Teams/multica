// @vitest-environment jsdom

import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import zhAgents from "../../locales/zh-Hans/agents.json";
import { useAgentAchievementCopy } from "./use-agent-achievement-copy";

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (bundle: typeof zhAgents) => unknown) =>
      String(selector(zhAgents) ?? ""),
  }),
}));

describe("useAgentAchievementCopy", () => {
  it("localizes every built-in achievement title", () => {
    const { result } = renderHook(() => useAgentAchievementCopy());
    const source = (id: string) => ({
      id,
      title: "Server title",
      description: "Server description",
      category: "delivery",
    });

    expect(
      [
        "first_launch",
        "proven_crew",
        "veteran_core",
        "centurion",
        "streak_5",
        "streak_20",
        "memory_spark",
        "memory_archive",
        "memory_constellation",
        "evolution_seed",
        "evolution_engine",
        "deep_space_explorer",
        "phoenix_protocol",
        "corvette_command",
        "cruiser_command",
        "dreadnought_command",
      ].map((id) => result.current(source(id)).title),
    ).toEqual([
      "星海首航",
      "砺炼成军",
      "百战中枢",
      "百夫长",
      "净焰连航",
      "不坠星轨",
      "记忆星火",
      "活态星库",
      "记忆星图",
      "演化星种",
      "演化引擎",
      "深空行者",
      "凤凰协议",
      "轻型护卫舰指挥官",
      "巡洋舰指挥官",
      "无畏舰统帅",
    ]);
  });

  it("localizes descriptions and categories, with future-id fallback", () => {
    const { result } = renderHook(() => useAgentAchievementCopy());

    expect(
      result.current({
        id: "first_launch",
        title: "First Launch",
        description: "Complete the first accepted task.",
        category: "delivery",
      }),
    ).toEqual({
      title: "星海首航",
      description: "首次完成一项通过验收的任务。",
      category: "交付",
    });
    expect(
      result.current({
        id: "future_badge",
        title: "Future Badge",
        description: "Future description",
        category: "future",
      }),
    ).toEqual({
      title: "Future Badge",
      description: "Future description",
      category: "future",
    });
  });

  it("does not reveal a locked secret achievement through its stable id", () => {
    const { result } = renderHook(() => useAgentAchievementCopy());

    expect(
      result.current({
        id: "dreadnought_command",
        title: "Secret achievement",
        description: "Keep developing to reveal this achievement.",
        category: "fleet",
        secret: true,
        unlocked: false,
      }),
    ).toEqual({
      title: "秘密成就",
      description: "继续积累贡献，解锁后即可揭晓。",
      category: "尚未揭晓",
    });
  });
});
