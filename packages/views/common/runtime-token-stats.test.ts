// @vitest-environment node
import { describe, expect, it } from "vitest";
import { runtimeTokenStatsLabel } from "./runtime-token-stats";

describe("runtimeTokenStatsLabel", () => {
  it("labels cache read as cache N (not opaque RN)", () => {
    expect(
      runtimeTokenStatsLabel({
        input_tokens: 68100,
        output_tokens: 7200,
        cache_read_tokens: 6,
      }),
    ).toBe("in 68.1k out 7.2k cache 6");
  });

  it("omits empty segments", () => {
    expect(
      runtimeTokenStatsLabel({
        input_tokens: 100,
        output_tokens: 0,
        cache_read_tokens: 0,
      }),
    ).toBe("in 100");
  });
});
