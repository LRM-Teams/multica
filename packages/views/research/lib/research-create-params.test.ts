// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  appendCreateParamsToGoal,
  defaultCreateParams,
  draftCreateParams,
  formatCreateParamsTrailer,
  normalizeCreateParams,
  parseCreateParamsFromGoal,
  resolveSessionCreateParams,
  stripCreateParamsTrailer,
  validateCreateComposer,
  validateCreateParams,
} from "./research-create-params";

describe("research-create-params (LRM-838 / LRM-835)", () => {
  it("defaults to standard depth and UI-locale language", () => {
    expect(defaultCreateParams("zh-Hans").depth_tier).toBe("standard");
    expect(defaultCreateParams("zh-Hans").language).toBe("zh");
    expect(defaultCreateParams("en").language).toBe("en");
    expect(defaultCreateParams("zh-Hans").source_weights.primary).toBe(0.85);
  });

  it("clamps and rounds weights in normalize (trailer/session path)", () => {
    const p = normalizeCreateParams({
      source_weights: { primary: 1.4, secondary: -0.2, community: 0.555 },
    });
    expect(p.source_weights.primary).toBe(1);
    expect(p.source_weights.secondary).toBe(0);
    expect(p.source_weights.community).toBe(0.56);
  });

  it("draft preserves out-of-range weights (no silent clamp)", () => {
    const d = draftCreateParams({
      source_weights: { primary: 1.4, secondary: -0.2, community: 0.5 },
    });
    expect(d.source_weights.primary).toBe(1.4);
    expect(d.source_weights.secondary).toBe(-0.2);
  });

  it("validateCreateParams blocks out-of-range weights and invalid depth", () => {
    const badWeights = validateCreateParams({
      source_weights: { primary: 1.4, secondary: 0.5, community: 0.2 },
    });
    expect(badWeights.ok).toBe(false);
    if (!badWeights.ok) {
      expect(badWeights.errors.weights?.primary).toBe("weight_out_of_range");
    }

    const badDepth = validateCreateParams({ depth_tier: "turbo" });
    expect(badDepth.ok).toBe(false);
    if (!badDepth.ok) {
      expect(badDepth.errors.depth).toBe("depth_invalid");
    }

    const ok = validateCreateParams({
      depth_tier: "deep",
      language: "zh",
      source_weights: { primary: 0.9, secondary: 0.5, community: 0.2 },
    });
    expect(ok.ok).toBe(true);
  });

  it("validateCreateComposer blocks empty goal without wiping params", () => {
    const params = defaultCreateParams("zh-Hans");
    const empty = validateCreateComposer({
      goal: "   ",
      hasTemplate: false,
      params,
    });
    expect(empty.ok).toBe(false);
    if (!empty.ok) {
      expect(empty.errors.goal).toBe("empty_goal");
    }

    const withTemplate = validateCreateComposer({
      goal: "",
      hasTemplate: true,
      params,
    });
    expect(withTemplate.ok).toBe(true);
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

  it("session resolve prefers durable contract, then trailer/defaults", () => {
    const legacyGoal = appendCreateParamsToGoal(
      "x",
      normalizeCreateParams({ depth_tier: "shallow", language: "en" }),
    );
    const withTrailer = resolveSessionCreateParams({
      goal: legacyGoal,
      depth_tier: "deep",
    });
    expect(withTrailer.depth_tier).toBe("deep");
    expect(withTrailer.language).toBe("en");

    const durable = resolveSessionCreateParams({
      goal: legacyGoal,
      depth_tier: "deep",
      contract: {
        language: "zh",
        source_policy: {
          weights: { primary: 0.95, secondary: 0.55, community: 0.15 },
        },
      },
    });
    expect(durable.language).toBe("zh");
    expect(durable.source_weights).toEqual({
      primary: 0.95,
      secondary: 0.55,
      community: 0.15,
    });

    const legacy = resolveSessionCreateParams({
      goal: "plain goal",
      depth_tier: "deep",
      uiLanguage: "zh",
    });
    expect(legacy.depth_tier).toBe("deep");
    expect(legacy.language).toBe("zh");
  });
});
