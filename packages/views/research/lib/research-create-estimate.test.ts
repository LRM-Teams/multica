import { describe, expect, it } from "vitest";
import { defaultCreateParams } from "./research-create-params";
import {
  lookupStaticCreateEstimate,
  resolveCreateEstimate,
} from "./research-create-estimate";

describe("research-create-estimate (LRM-839)", () => {
  it("returns ready estimates for default params", () => {
    const result = resolveCreateEstimate(defaultCreateParams("zh-Hans"));
    expect(result.status).toBe("ready");
    if (result.status !== "ready") return;
    expect(result.estimate.duration_min).toBeLessThan(result.estimate.duration_max);
    expect(["low", "medium", "high"]).toContain(result.estimate.cost_tier);
  });

  it("links depth changes: deep is longer/higher than shallow", () => {
    const base = defaultCreateParams("en");
    const shallow = resolveCreateEstimate({ ...base, depth_tier: "shallow" });
    const deep = resolveCreateEstimate({ ...base, depth_tier: "deep" });
    expect(shallow.status).toBe("ready");
    expect(deep.status).toBe("ready");
    if (shallow.status !== "ready" || deep.status !== "ready") return;
    expect(deep.estimate.duration_min).toBeGreaterThan(shallow.estimate.duration_max);
    expect(deep.estimate.cost_tier).toBe("high");
    expect(shallow.estimate.cost_tier).toBe("low");
  });

  it("links source weights and language without needing submit", () => {
    const base = defaultCreateParams("zh-Hans");
    const light = resolveCreateEstimate({
      ...base,
      source_weights: { primary: 0.2, secondary: 0.2, community: 0.2 },
    });
    const heavyEn = resolveCreateEstimate({
      ...base,
      language: "en",
      source_weights: { primary: 0.95, secondary: 0.9, community: 0.85 },
    });
    expect(light.status).toBe("ready");
    expect(heavyEn.status).toBe("ready");
    if (light.status !== "ready" || heavyEn.status !== "ready") return;
    expect(heavyEn.estimate.duration_max).toBeGreaterThan(light.estimate.duration_max);
  });

  it("resolves synchronously (AC ≤200ms visible update)", () => {
    const start = performance.now();
    for (let i = 0; i < 200; i++) {
      resolveCreateEstimate({
        ...defaultCreateParams("en"),
        depth_tier: i % 2 === 0 ? "standard" : "deep",
      });
    }
    expect(performance.now() - start).toBeLessThan(200);
  });

  it("shows unknown for invalid depth and does not throw", () => {
    const result = resolveCreateEstimate({
      ...defaultCreateParams("en"),
      depth_tier: "turbo",
    });
    expect(result).toEqual({ status: "unknown", reason: "invalid_params" });
  });

  it("shows unknown for non-finite weights", () => {
    const base = defaultCreateParams("en");
    const result = resolveCreateEstimate({
      ...base,
      source_weights: { ...base.source_weights, primary: Number.NaN },
    });
    expect(result).toEqual({ status: "unknown", reason: "invalid_params" });
  });

  it("shows unknown when lookup returns null (no data)", () => {
    const result = resolveCreateEstimate(defaultCreateParams("en"), {
      lookup: () => null,
    });
    expect(result).toEqual({ status: "unknown", reason: "no_data" });
  });

  it("static lookup covers every depth tier", () => {
    for (const depth_tier of ["shallow", "standard", "deep"] as const) {
      const estimate = lookupStaticCreateEstimate({
        ...defaultCreateParams("zh-Hans"),
        depth_tier,
      });
      expect(estimate).not.toBeNull();
    }
  });
});
