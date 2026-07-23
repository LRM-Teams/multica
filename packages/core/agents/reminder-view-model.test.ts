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
      nextFireAt: "2026-07-24T09:00:00Z",
    });
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
  // schema deliberately keeps schedule_kind as a plain string so an
  // unrecognized future value doesn't reject the WHOLE page — this proves
  // the row-level boundary drops it instead of misclassifying it as either
  // known state.
  it("drops the row instead of misclassifying an unrecognized schedule_kind", () => {
    // @ts-expect-error -- exercising a value outside the narrow TS union to
    // prove the runtime guard, not just the type system.
    const row = adaptUpcomingRow(makeDefinition({ schedule_kind: "some_future_kind" }));
    expect(row).toBeNull();
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
      firedAt: "2026-07-23T09:00:05Z",
      definitionStatus: "scheduled",
    });
  });

  it("returns null when fired_at is missing", () => {
    expect(adaptFiredRow(makeOccurrence({ fired_at: "" }))).toBeNull();
  });

  it("drops the row instead of misclassifying an unrecognized schedule_kind", () => {
    // @ts-expect-error -- see the equivalent adaptUpcomingRow case above.
    const row = adaptFiredRow(makeOccurrence({ schedule_kind: "some_future_kind" }));
    expect(row).toBeNull();
  });

  it("drops the row instead of misclassifying an unrecognized definition_status", () => {
    // @ts-expect-error -- see the equivalent adaptUpcomingRow case above.
    const row = adaptFiredRow(makeOccurrence({ definition_status: "some_future_status" }));
    expect(row).toBeNull();
  });
});
