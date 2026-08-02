import { describe, expect, it } from "vitest";
import {
  appendCreateParamsToGoal,
  defaultCreateParams,
  formatCreateParamsTrailer,
  normalizeCreateParams,
  parseCreateParamsFromGoal,
  resolveSessionCreateParams,
  stripCreateParamsTrailer,
} from "./research-create-params";

describe("research-create-params (LRM-838)", () => {
  it("defaults to standard depth and UI-locale language", () => {
    expect(defaultCreateParams("zh-Hans").depth_tier).toBe("standard");
    expect(defaultCreateParams("zh-Hans").language).toBe("zh");
    expect(defaultCreateParams("en").language).toBe("en");
    expect(defaultCreateParams("zh-Hans").source_weights.primary).toBe(0.85);
  });

  it("clamps and rounds weights", () => {
    const p = normalizeCreateParams({
      source_weights: { primary: 1.4, secondary: -0.2, community: 0.555 },
    });
    expect(p.source_weights.primary).toBe(1);
    expect(p.source_weights.secondary).toBe(0);
    expect(p.source_weights.community).toBe(0.56);
  });

  it("round-trips a trailer on the goal", () => {
    const params = normalizeCreateParams({
      depth_tier: "deep",
      language: "zh",
      source_weights: { primary: 0.9, secondary: 0.5, community: 0.2 },
    });
    const goal = appendCreateParamsToGoal("调研向量数据库", params);
    expect(goal).toContain("调研向量数据库");
    expect(goal).toContain(formatCreateParamsTrailer(params));
    expect(parseCreateParamsFromGoal(goal)).toEqual(params);
    expect(stripCreateParamsTrailer(goal)).toBe("调研向量数据库");
  });

  it("replaces an existing trailer instead of stacking", () => {
    const first = appendCreateParamsToGoal(
      "goal",
      normalizeCreateParams({ depth_tier: "shallow" }),
    );
    const second = appendCreateParamsToGoal(
      first,
      normalizeCreateParams({ depth_tier: "deep", language: "en" }),
    );
    expect(second.match(/【调研参数/g)?.length).toBe(1);
    expect(parseCreateParamsFromGoal(second)?.depth_tier).toBe("deep");
    expect(parseCreateParamsFromGoal(second)?.language).toBe("en");
  });

  it("session resolve prefers trailer, then session depth_tier", () => {
    const withTrailer = resolveSessionCreateParams({
      goal: appendCreateParamsToGoal(
        "x",
        normalizeCreateParams({ depth_tier: "shallow", language: "en" }),
      ),
      depth_tier: "deep",
    });
    expect(withTrailer.depth_tier).toBe("deep");
    expect(withTrailer.language).toBe("en");

    const legacy = resolveSessionCreateParams({
      goal: "plain goal",
      depth_tier: "deep",
      uiLanguage: "zh",
    });
    expect(legacy.depth_tier).toBe("deep");
    expect(legacy.language).toBe("zh");
  });
});
