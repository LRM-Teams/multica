// @vitest-environment jsdom

import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import zhSettings from "../locales/zh-Hans/settings.json";
import {
  HUMAN_HONOR_BADGE_IDS,
  useHonorBadgeCopy,
} from "./use-honor-badge-copy";

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (
      selector: (bundle: typeof zhSettings) => unknown,
      options?: Record<string, string | number>,
    ) => {
      const template = selector(zhSettings);
      if (typeof template !== "string") return String(template ?? "");
      return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(options?.[key] ?? `{{${key}}}`),
      );
    },
  }),
}));

describe("useHonorBadgeCopy", () => {
  it("localizes every built-in human honor badge title", () => {
    const { result } = renderHook(() => useHonorBadgeCopy());

    expect(HUMAN_HONOR_BADGE_IDS).toHaveLength(51);
    expect(
      HUMAN_HONOR_BADGE_IDS.map((id) =>
        result.current({
          id,
          title: "Server title",
          description: "Server description",
          unlocked: true,
        }).title,
      ),
    ).toEqual([
      "创世星云",
      "星尘初光",
      "水星疾影",
      "金星辉耀",
      "蓝星锚点",
      "火星拓荒者",
      "木星引力",
      "土星光环",
      "天王星斜辉",
      "海王星领航者",
      "冥王远征",
      "赤色巨星",
      "恒燃红矮星",
      "蓝色巨星",
      "类星灯塔",
      "星环铸造者",
      "双星协奏",
      "月海星火",
      "彗星航迹",
      "星岩斥候",
      "星蚀守望者",
      "脉冲星讯",
      "日帆远航者",
      "轨道新锐",
      "月城筑造师",
      "星路先驱",
      "天际旅者",
      "星炬守望者",
      "星链领航者",
      "星档之种",
      "星座图谱",
      "极光织师",
      "银河漫游者",
      "虫洞绘界师",
      "星土重塑者",
      "熔星之心",
      "星枢纽带",
      "螺旋智核",
      "棱镜核心",
      "等离子星核",
      "量子之门",
      "奇点",
      "天穹之冠",
      "事件视界",
      "宇宙之树",
      "无垠引擎",
      "星讯架构师",
      "星纪引擎",
      "恒光",
      "永恒在场",
      "交付奇点",
    ]);
  });

  it("localizes descriptions, unlock rules, and progress labels", () => {
    const { result } = renderHook(() => useHonorBadgeCopy());

    expect(
      result.current({
        id: "stardust",
        title: "Stardust",
        description: "Reached level 3.",
        unlock_rule: "Reach honor level 3",
        progress: { current: 2, target: 3, label: "level" },
        unlocked: false,
      }),
    ).toMatchObject({
      title: "星尘初光",
      description: "达到 3 级，旅程自星尘间启航。",
      unlockRule: "达到荣誉等级 3",
      progressLabel: "荣誉等级",
    });

    expect(
      result.current({
        id: "builder",
        title: "Forge Ring",
        description: "Delivery pillar tier 4 or higher.",
        unlock_rule: "Delivery pillar tier 4+",
        progress: { current: 3, target: 4, label: "delivery" },
        unlocked: false,
      }),
    ).toMatchObject({
      title: "星环铸造者",
      unlockRule: "成果交付达到第 4 阶",
      progressLabel: "成果交付",
    });
  });

  it("keeps secret achievements redacted and preserves future server entries", () => {
    const { result } = renderHook(() => useHonorBadgeCopy());

    expect(
      result.current({
        id: "quantum_gate",
        title: "Secret Badge",
        description: "Unlock to reveal this badge.",
        secret: true,
        unlocked: false,
      }),
    ).toMatchObject({
      title: "秘密成就",
      description: "继续积累贡献，解锁后即可揭晓。",
      unlockRule: "",
    });

    expect(
      result.current({
        id: "future_badge",
        title: "Future Badge",
        description: "Future description",
        unlock_rule: "Future rule",
        progress: { current: 1, target: 2, label: "future" },
      }),
    ).toEqual({
      title: "Future Badge",
      description: "Future description",
      unlockRule: "Future rule",
      progressLabel: "future",
    });
  });
});
