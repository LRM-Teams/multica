import { describe, expect, it } from "vitest";
import { formatTimeAgo, timeAgo } from "./time-ago";

// Reference "now": 2026-07-06T20:00:00Z.
const NOW = Date.parse("2026-07-06T20:00:00Z");

// LRM-763 relative contract (mirrors packages/views useTimeAgo, English-only
// on mobile v1): Just now / Nm ago / Nh ago / Nd ago — no weeks bucket, no
// toLocale* fallback.
describe("formatTimeAgo", () => {
  it("under a minute -> Just now", () => {
    expect(formatTimeAgo(NOW - 30_000, NOW)).toBe("Just now");
  });

  it("future timestamps / clock skew -> Just now", () => {
    expect(formatTimeAgo(NOW + 5 * 60_000, NOW)).toBe("Just now");
  });

  it("1..59 minutes -> Nm ago", () => {
    expect(formatTimeAgo(NOW - 60_000, NOW)).toBe("1m ago");
    expect(formatTimeAgo(NOW - 42 * 60_000, NOW)).toBe("42m ago");
  });

  it("1..23 hours -> Nh ago", () => {
    expect(formatTimeAgo(NOW - 60 * 60_000, NOW)).toBe("1h ago");
    expect(formatTimeAgo(NOW - 23 * 60 * 60_000, NOW)).toBe("23h ago");
  });

  it("24h+ -> Nd ago, uncapped (no weeks bucket, no date fallback)", () => {
    expect(formatTimeAgo(NOW - 24 * 60 * 60_000, NOW)).toBe("1d ago");
    expect(formatTimeAgo(NOW - 6 * 86_400_000, NOW)).toBe("6d ago");
    expect(formatTimeAgo(NOW - 30 * 86_400_000, NOW)).toBe("30d ago");
  });

  it("invalid timestamp -> empty string (never 'NaNm ago')", () => {
    expect(formatTimeAgo(NaN, NOW)).toBe("");
    expect(timeAgo("not-a-date")).toBe("");
  });
});
