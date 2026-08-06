import { describe, expect, it } from "vitest";
// Next.js's own path-to-regexp build, so this test exercises the exact
// matching engine `redirects()` runs through — not an approximation.
import { pathToRegexp } from "next/dist/compiled/path-to-regexp";

import { RUNTIMES_TO_COMPUTERS_REDIRECTS } from "./route-redirects";

describe("RUNTIMES_TO_COMPUTERS_REDIRECTS", () => {
  const [pageRedirect, detailRedirect] = RUNTIMES_TO_COMPUTERS_REDIRECTS;

  it("never matches /api/runtimes — a page redirect must not swallow API routes", () => {
    expect(pathToRegexp(pageRedirect.source).test("/api/runtimes")).toBe(false);
    expect(pathToRegexp(detailRedirect.source).test("/api/runtimes/rt-1")).toBe(false);
  });

  it("still matches real workspace paths", () => {
    expect(pathToRegexp(pageRedirect.source).test("/lrm-team/runtimes")).toBe(true);
    expect(pathToRegexp(detailRedirect.source).test("/lrm-team/runtimes/rt-1")).toBe(true);
  });

  it("does not over-exclude workspace slugs that merely start with 'api'", () => {
    expect(pathToRegexp(pageRedirect.source).test("/apitest/runtimes")).toBe(true);
    expect(pathToRegexp(detailRedirect.source).test("/apitest/runtimes/rt-1")).toBe(true);
  });
});
