import { describe, expect, it } from "vitest";
import {
  formatReminderAbsolute,
  formatReminderCadence,
  formatReminderRelative,
  isBareShortIdAnchorLabel,
  deriveReminderStatusChip,
  type ReminderCadenceLabels,
} from "./reminder-display";

const EN_LABELS: ReminderCadenceLabels = {
  oneShot: "One-time",
  daily: (time, timezone) => `daily at ${time}${timezone ? ` ${timezone}` : ""}`,
  weekly: (days, time, timezone) =>
    `weekly ${days} at ${time}${timezone ? ` ${timezone}` : ""}`,
  everyMinutes: (count) => `every ${count} minutes`,
  everyHours: (count) => `every ${count} hours`,
  everyDays: (count) => `every ${count} days`,
  raw: (description, timezone) => `${description}${timezone ? ` ${timezone}` : ""}`,
};

describe("formatReminderRelative", () => {
  const now = Date.parse("2026-07-23T14:26:00Z");

  it("formats a near-future fire as 'in N minutes'", () => {
    expect(formatReminderRelative("2026-07-23T14:29:00Z", "en", now)).toBe("in 3 minutes");
  });

  it("formats a same-day future fire in hours", () => {
    expect(formatReminderRelative("2026-07-24T02:00:00Z", "en", now)).toBe("in 12 hours");
  });

  it("formats a past fire as 'N ago'", () => {
    expect(formatReminderRelative("2026-07-23T11:26:00Z", "en", now)).toBe("3 hours ago");
  });
});

describe("formatReminderAbsolute", () => {
  it("matches Raft-style 'Jul 23 at 22:29' in en + Asia/Shanghai", () => {
    // 2026-07-23T14:29:00Z == Jul 23 22:29 Asia/Shanghai
    expect(
      formatReminderAbsolute("2026-07-23T14:29:00Z", "en", "Asia/Shanghai", "at"),
    ).toBe("Jul 23 at 22:29");
  });

  it("omits the connector when atWord is empty (zh/ja/ko)", () => {
    expect(
      formatReminderAbsolute("2026-07-23T14:29:00Z", "en", "UTC", ""),
    ).toBe("Jul 23 14:29");
  });
});

describe("formatReminderCadence", () => {
  it("labels one-shot reminders", () => {
    expect(formatReminderCadence({ kind: "one_shot" }, EN_LABELS)).toBe("One-time");
  });

  it("humanizes daily@HH:MM with locked timezone (Frank reference line)", () => {
    expect(
      formatReminderCadence(
        {
          kind: "recurring",
          family: "calendar",
          description: "daily@10:00",
          timezone: "Asia/Shanghai",
        },
        EN_LABELS,
      ),
    ).toBe("daily at 10:00 Asia/Shanghai");
  });

  it("humanizes weekly:days@HH:MM", () => {
    expect(
      formatReminderCadence(
        {
          kind: "recurring",
          family: "calendar",
          description: "weekly:mon,fri@09:00",
          timezone: "UTC",
        },
        EN_LABELS,
      ),
    ).toBe("weekly mon, fri at 09:00 UTC");
  });

  it("humanizes every:Nm interval without inventing a timezone", () => {
    expect(
      formatReminderCadence(
        { kind: "recurring", family: "interval", description: "every:30m" },
        EN_LABELS,
      ),
    ).toBe("every 30 minutes");
  });
});

describe("isBareShortIdAnchorLabel", () => {
  it("rejects Raft-style bare #multica:<shortid> and #workspace:<shortid>", () => {
    expect(isBareShortIdAnchorLabel("#multica:1c5652c2")).toBe(true);
    expect(isBareShortIdAnchorLabel("  #multica:528d7cee  ")).toBe(true);
    expect(isBareShortIdAnchorLabel("#workspace:abcdef12")).toBe(true);
  });

  it("allows readable channel / DM labels", () => {
    expect(isBareShortIdAnchorLabel("#deploys")).toBe(false);
    expect(isBareShortIdAnchorLabel("Direct message")).toBe(false);
    expect(isBareShortIdAnchorLabel("Thread in #standup")).toBe(false);
  });
});

describe("deriveReminderStatusChip", () => {
  const now = Date.parse("2026-07-23T14:26:00Z");

  it("marks upcoming future fires as scheduled", () => {
    expect(
      deriveReminderStatusChip({
        section: "upcoming",
        definitionStatus: "scheduled",
        fireAtIso: "2026-07-23T14:29:00Z",
        nowMs: now,
      }),
    ).toBe("scheduled");
  });

  it("marks upcoming past-due fires as overdue", () => {
    expect(
      deriveReminderStatusChip({
        section: "upcoming",
        definitionStatus: "scheduled",
        fireAtIso: "2026-07-23T14:00:00Z",
        nowMs: now,
      }),
    ).toBe("overdue");
  });

  it("marks history occurrences as fired unless the definition is cancelled", () => {
    expect(
      deriveReminderStatusChip({
        section: "history",
        definitionStatus: "scheduled",
        fireAtIso: "2026-07-21T01:00:00Z",
        nowMs: now,
      }),
    ).toBe("fired");
    expect(
      deriveReminderStatusChip({
        section: "history",
        definitionStatus: "cancelled",
        fireAtIso: "2026-07-21T01:00:00Z",
        nowMs: now,
      }),
    ).toBe("cancelled");
  });
});
