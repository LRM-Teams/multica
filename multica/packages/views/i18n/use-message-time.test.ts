// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  formatListTime,
  formatMessageTime,
  fullTimestamp,
  messageDayLabel,
  startsNewLocalDay,
} from "./use-message-time";

const LABELS = { today: "Today", yesterday: "Yesterday" };
// Reference "now": 2026-07-06T20:00:00Z (a Monday). In Asia/Shanghai (UTC+8)
// that is 2026-07-07 04:00, i.e. the *next* local day — used to prove the
// formatter buckets by the viewing timezone, not UTC.
const NOW = Date.parse("2026-07-06T20:00:00Z");

describe("formatMessageTime", () => {
  it("today -> HH:MM in 24-hour, no AM/PM", () => {
    expect(formatMessageTime(Date.parse("2026-07-06T09:36:00Z"), NOW, "UTC", LABELS)).toBe("09:36");
    // Evening reads as 21:05, never 9:05 PM.
    expect(formatMessageTime(Date.parse("2026-07-06T21:05:00Z"), NOW, "UTC", LABELS)).toBe("21:05");
  });

  it("yesterday -> '{yesterday} HH:MM'", () => {
    expect(formatMessageTime(Date.parse("2026-07-05T09:36:00Z"), NOW, "UTC", LABELS)).toBe("Yesterday 09:36");
  });

  it("earlier this year -> MM/DD HH:MM (zero-padded)", () => {
    expect(formatMessageTime(Date.parse("2026-03-05T09:36:00Z"), NOW, "UTC", LABELS)).toBe("03/05 09:36");
  });

  it("previous years -> YYYY/MM/DD HH:MM", () => {
    expect(formatMessageTime(Date.parse("2025-12-30T09:36:00Z"), NOW, "UTC", LABELS)).toBe("2025/12/30 09:36");
  });

  it("buckets by the viewing timezone, not UTC", () => {
    const value = Date.parse("2026-07-06T15:00:00Z");
    // UTC: same day as NOW -> today -> just the time.
    expect(formatMessageTime(value, NOW, "UTC", LABELS)).toBe("15:00");
    // Shanghai (UTC+8): value = 07-06 23:00, now = 07-07 04:00 -> yesterday.
    expect(formatMessageTime(value, NOW, "Asia/Shanghai", LABELS)).toBe("Yesterday 23:00");
  });
});

// LRM-763 sidebar-list contract: today -> HH:MM; yesterday -> 昨天;
// 2..6 days -> weekday; older -> date. No clock on day-granular buckets.
describe("formatListTime", () => {
  // NOW = 2026-07-06T20:00Z, a Monday in UTC.
  it("today -> HH:MM only", () => {
    expect(formatListTime(Date.parse("2026-07-06T09:36:00Z"), NOW, "UTC", "en", LABELS)).toBe("09:36");
  });

  it("yesterday -> {yesterday} with no clock", () => {
    expect(formatListTime(Date.parse("2026-07-05T09:36:00Z"), NOW, "UTC", "en", LABELS)).toBe("Yesterday");
  });

  it("2..6 local days ago -> localized weekday", () => {
    // 2026-07-04 was a Saturday, 2026-07-01 a Wednesday.
    expect(formatListTime(Date.parse("2026-07-04T09:36:00Z"), NOW, "UTC", "en", LABELS)).toBe("Saturday");
    expect(formatListTime(Date.parse("2026-07-01T09:36:00Z"), NOW, "UTC", "zh-Hans", LABELS)).toBe("星期三");
  });

  it("7+ days ago, same year -> MM/DD", () => {
    expect(formatListTime(Date.parse("2026-06-28T09:36:00Z"), NOW, "UTC", "en", LABELS)).toBe("06/28");
  });

  it("exactly 7 local days ago -> MM/DD, not weekday", () => {
    // 2026-06-29 is a Monday — the same weekday as NOW. Labeling it "Monday"
    // would collide with today's bucket, so the weekday ladder is 2..6 days
    // only and day 7 falls through to the date (LRM-763 contract).
    expect(formatListTime(Date.parse("2026-06-29T09:36:00Z"), NOW, "UTC", "en", LABELS)).toBe("06/29");
  });

  it("previous years -> YYYY/MM/DD", () => {
    expect(formatListTime(Date.parse("2025-12-30T09:36:00Z"), NOW, "UTC", "en", LABELS)).toBe("2025/12/30");
  });

  it("buckets by the viewing timezone, not UTC", () => {
    const value = Date.parse("2026-07-06T15:00:00Z");
    // UTC: same day as NOW -> clock.
    expect(formatListTime(value, NOW, "UTC", "en", LABELS)).toBe("15:00");
    // Shanghai: value = 07-06 23:00 local, now = 07-07 04:00 local -> yesterday.
    expect(formatListTime(value, NOW, "Asia/Shanghai", "en", LABELS)).toBe("Yesterday");
  });
});

describe("startsNewLocalDay", () => {
  it("is true for the first row (no previous)", () => {
    expect(startsNewLocalDay(Date.parse("2026-07-06T09:00:00Z"), null, "UTC")).toBe(true);
  });

  it("is false within the same local day", () => {
    expect(
      startsNewLocalDay(
        Date.parse("2026-07-06T18:00:00Z"),
        Date.parse("2026-07-06T09:00:00Z"),
        "UTC",
      ),
    ).toBe(false);
  });

  it("is true across a local day boundary", () => {
    expect(
      startsNewLocalDay(
        Date.parse("2026-07-06T09:00:00Z"),
        Date.parse("2026-07-05T22:00:00Z"),
        "UTC",
      ),
    ).toBe(true);
  });
});

describe("messageDayLabel", () => {
  it("uses today/yesterday labels", () => {
    expect(messageDayLabel(Date.parse("2026-07-06T09:00:00Z"), NOW, "UTC", "en", LABELS)).toBe("Today");
    expect(messageDayLabel(Date.parse("2026-07-05T09:00:00Z"), NOW, "UTC", "en", LABELS)).toBe("Yesterday");
  });

  it("spells the weekday + date for older days, adding the year cross-year", () => {
    const thisYear = messageDayLabel(Date.parse("2026-03-05T09:00:00Z"), NOW, "UTC", "en", LABELS);
    expect(thisYear).toContain("March");
    expect(thisYear).not.toContain("2026");
    const crossYear = messageDayLabel(Date.parse("2025-12-30T09:00:00Z"), NOW, "UTC", "en", LABELS);
    expect(crossYear).toContain("2025");
  });
});

describe("fullTimestamp", () => {
  it("renders a locale-aware absolute date + time (24-hour)", () => {
    const full = fullTimestamp(Date.parse("2026-07-06T09:36:00Z"), "UTC", "en");
    expect(full).toContain("2026");
    expect(full).toContain("09:36");
    expect(full).not.toMatch(/AM|PM/);
  });
});
