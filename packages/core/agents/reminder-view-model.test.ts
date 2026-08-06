import { describe, it, expect } from "vitest";
import {
  adaptUpcomingRow,
  adaptFiredRow,
  type RawReminderDefinition,
  type RawReminderOccurrence,
} from "./reminder-view-model";

function makeDefinition(overrides: Partial<RawReminderDefinition> = {}): RawReminderDefinition {
  return {
    id: "r-1",
    title: "Ping standup",
    status: "scheduled",
    schedule_kind: "recurring",
    next_fire_at: "2026-07-24T09:00:00Z",
    cadence: "daily@09:00",
    schedule_timezone: "America/Los_Angeles",
    snooze_count: 0,
    origin_kind: "agent",
    anchor: { available: false },
    ...overrides,
  };
}

function makeOccurrence(overrides: Partial<RawReminderOccurrence> = {}): RawReminderOccurrence {
  return {
    id: "o-1",
    reminder_id: "r-1",
    title: "Ping standup",
    status: "fired",
    definition_status: "scheduled",
    schedule_kind: "recurring",
    cadence_scheduled_for: "2026-07-23T09:00:00Z",
    due_at: "2026-07-23T09:00:00Z",
    fired_at: "2026-07-23T09:00:05Z",
    cadence: "daily@09:00",
    schedule_timezone: "America/Los_Angeles",
    anchor: { available: false },
    ...overrides,
  };
}

describe("adaptUpcomingRow", () => {
  it("adapts a well-formed recurring row", () => {
    const row = adaptUpcomingRow(makeDefinition());
    expect(row).toEqual({
      id: "r-1",
      title: "Ping standup",
      cadence: {
        kind: "recurring",
        family: "calendar",
        description: "daily@09:00",
        timezone: "America/Los_Angeles",
      },
      anchor: { available: false },
      origin: { kind: "agent" },
      nextFireAt: "2026-07-24T09:00:00Z",
      lastFireAt: undefined,
      status: "scheduled",
    });
  });

  it("does not admit an ordinary fired definition into the visible definition list", () => {
    expect(
      adaptUpcomingRow(
        makeDefinition({
          status: "fired",
          next_fire_at: undefined,
          origin_kind: "agent",
        }),
      ),
    ).toBeNull();
  });

  it("drops an unknown or malformed origin rather than hiding its source", () => {
    expect(adaptUpcomingRow(makeDefinition({ origin_kind: "future_origin" }))).toBeNull();
    expect(adaptUpcomingRow(makeDefinition({ origin_kind: "group_manager_auto" }))).toBeNull();
  });

  it("degrades bare #workspace:shortId anchor labels to unavailable", () => {
    const row = adaptUpcomingRow(
      makeDefinition({
        anchor: {
          available: true,
          kind: "channel",
          display: "#multica:a1b2c3d4",
          href: "/acme/channels/chan-1?message=msg-1",
        },
      }),
    );
    expect(row?.anchor).toEqual({ available: false });
  });

  it("uses display_name only and ignores legacy display (LRM-238)", () => {
    const row = adaptUpcomingRow(
      makeDefinition({
        anchor: {
          available: true,
          kind: "channel",
          display_name: "#deploys",
          display: "#multica:a1b2c3d4",
          href: "/acme/channels/chan-1?message=msg-1",
        },
      }),
    );
    expect(row?.anchor).toEqual({
      available: true,
      kind: "channel",
      label: "#deploys",
      href: "/acme/channels/chan-1?message=msg-1",
    });
  });

  it("degrades when display_name itself is a bare short id", () => {
    const row = adaptUpcomingRow(
      makeDefinition({
        anchor: {
          available: true,
          kind: "channel",
          display_name: "#multica:deadbeef",
          href: "/acme/channels/chan-1?message=msg-1",
        },
      }),
    );
    expect(row?.anchor).toEqual({ available: false });
  });

  it("does not fall back to legacy display when display_name is absent (LRM-238)", () => {
    const row = adaptUpcomingRow(
      makeDefinition({
        anchor: {
          available: true,
          kind: "thread",
          display: "Thread in #deploys",
          href: "/acme/channels/chan-1?message=msg-1",
        },
      }),
    );
    expect(row?.anchor).toEqual({ available: false });
  });

  it("adapts a well-formed one_shot row", () => {
    const row = adaptUpcomingRow(
      makeDefinition({ schedule_kind: "one_shot", cadence: undefined, schedule_timezone: undefined }),
    );
    expect(row?.cadence).toEqual({ kind: "one_shot" });
  });

  it("returns null when next_fire_at is missing", () => {
    expect(adaptUpcomingRow(makeDefinition({ next_fire_at: "" }))).toBeNull();
  });

  // #656 API Response Compatibility hardening (Parker review): the runtime
  // schema deliberately keeps schedule_kind/status as plain strings so an
  // unrecognized future value doesn't reject the WHOLE page — these prove
  // the row-level boundary drops the row instead of misclassifying it.
  it("drops the row instead of misclassifying an unrecognized schedule_kind", () => {
    const row = adaptUpcomingRow(makeDefinition({ schedule_kind: "some_future_kind" }));
    expect(row).toBeNull();
  });

  it("drops a recurring row with no cadence string instead of downgrading it to one_shot", () => {
    const row = adaptUpcomingRow(makeDefinition({ schedule_kind: "recurring", cadence: undefined }));
    expect(row).toBeNull();
  });

  it("drops the row when status is outside the Upcoming section's scheduled|firing contract", () => {
    expect(adaptUpcomingRow(makeDefinition({ status: "fired" }))).toBeNull();
    expect(adaptUpcomingRow(makeDefinition({ status: "cancelled" }))).toBeNull();
    expect(adaptUpcomingRow(makeDefinition({ status: "some_future_status" }))).toBeNull();
  });

  it("prefers server display_name over the legacy display alias", () => {
    const row = adaptUpcomingRow(
      makeDefinition({
        anchor: {
          available: true,
          kind: "channel",
          display_name: "# LRM2.0开发群",
          display: "# legacy",
          href: "/w/channels/c",
        },
      }),
    );
    expect(row?.anchor).toEqual({
      available: true,
      kind: "channel",
      label: "# LRM2.0开发群",
      href: "/w/channels/c",
    });
  });

  it("keeps the row but degrades the anchor to unavailable for an unrecognized anchor kind", () => {
    const row = adaptUpcomingRow(
      makeDefinition({
        anchor: { available: true, kind: "some_future_kind", display: "x", href: "/x" },
      }),
    );
    expect(row).not.toBeNull();
    expect(row?.anchor).toEqual({ available: false });
  });
});

describe("adaptFiredRow", () => {
  it("adapts a well-formed row", () => {
    const row = adaptFiredRow(makeOccurrence());
    expect(row).toEqual({
      id: "o-1",
      title: "Ping standup",
      cadence: {
        kind: "recurring",
        family: "calendar",
        description: "daily@09:00",
        timezone: "America/Los_Angeles",
      },
      anchor: { available: false },
      origin: { kind: "agent" },
      firedAt: "2026-07-23T09:00:05Z",
      definitionStatus: "scheduled",
    });
  });

  it("returns null when fired_at is missing", () => {
    expect(adaptFiredRow(makeOccurrence({ fired_at: "" }))).toBeNull();
  });

  it("drops the row instead of misclassifying an unrecognized schedule_kind", () => {
    const row = adaptFiredRow(makeOccurrence({ schedule_kind: "some_future_kind" }));
    expect(row).toBeNull();
  });

  it("drops a recurring row with no cadence string instead of downgrading it to one_shot", () => {
    const row = adaptFiredRow(makeOccurrence({ schedule_kind: "recurring", cadence: undefined }));
    expect(row).toBeNull();
  });

  it("drops the row instead of misclassifying an unrecognized definition_status", () => {
    const row = adaptFiredRow(makeOccurrence({ definition_status: "some_future_status" }));
    expect(row).toBeNull();
  });

  it("drops the row when occurrence status is anything other than fired (the History section's own contract)", () => {
    expect(adaptFiredRow(makeOccurrence({ status: "scheduled" }))).toBeNull();
    expect(adaptFiredRow(makeOccurrence({ status: "some_future_status" }))).toBeNull();
  });

  it("keeps the row but degrades the anchor to unavailable for an unrecognized anchor kind", () => {
    const row = adaptFiredRow(
      makeOccurrence({
        anchor: { available: true, kind: "some_future_kind", display: "x", href: "/x" },
      }),
    );
    expect(row).not.toBeNull();
    expect(row?.anchor).toEqual({ available: false });
  });
});
