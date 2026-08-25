import { describe, expect, it } from "vitest";
import {
  toUpcomingReminderRow,
  type AgentReminderDefinitionResponse,
} from "./reminder-view-model";

function definition(
  overrides: Partial<AgentReminderDefinitionResponse> = {},
): AgentReminderDefinitionResponse {
  return {
    id: "reminder-1",
    title: "Ping standup",
    status: "scheduled",
    scheduleKind: "recurring",
    nextFireAt: "2099-07-24T09:00:00Z",
    cadence: "daily@09:00",
    scheduleTimezone: "Asia/Shanghai",
    snoozeCount: 0,
    anchor: { available: false },
    ...overrides,
  };
}

describe("toUpcomingReminderRow", () => {
  it("adapts scheduled definitions", () => {
    expect(toUpcomingReminderRow(definition())).toEqual({
      id: "reminder-1",
      title: "Ping standup",
      cadence: {
        kind: "recurring",
        family: "calendar",
        description: "daily@09:00",
        timezone: "Asia/Shanghai",
      },
      anchor: { available: false },
      origin: { kind: "agent" },
      nextFireAt: "2099-07-24T09:00:00Z",
      lastFireAt: undefined,
      status: "scheduled",
    });
  });

  it("drops terminal definitions", () => {
    expect(toUpcomingReminderRow(definition({ status: "fired" }))).toBeNull();
    expect(toUpcomingReminderRow(definition({ status: "cancelled" }))).toBeNull();
  });

  it("uses the authorized display name for anchors", () => {
    expect(
      toUpcomingReminderRow(
        definition({
          anchor: {
            available: true,
            kind: "channel",
            displayName: "#deploys",
            href: "/acme/channels/channel-1?message=message-1",
          },
        }),
      )?.anchor,
    ).toEqual({
      available: true,
      kind: "channel",
      label: "#deploys",
      href: "/acme/channels/channel-1?message=message-1",
    });
  });

  it("degrades unnamed and bare short-id anchors", () => {
    expect(
      toUpcomingReminderRow(
        definition({
          anchor: {
            available: true,
            kind: "channel",
            displayName: "",
            href: "/acme/channels/channel-1",
          },
        }),
      )?.anchor,
    ).toEqual({ available: false });
    expect(
      toUpcomingReminderRow(
        definition({
          anchor: {
            available: true,
            kind: "channel",
            displayName: "#multica:deadbeef",
            href: "/acme/channels/channel-1",
          },
        }),
      )?.anchor,
    ).toEqual({ available: false });
  });

  it("adapts one-shot definitions and rejects malformed recurring definitions", () => {
    expect(
      toUpcomingReminderRow(
        definition({
          scheduleKind: "one_shot",
          cadence: undefined,
          scheduleTimezone: undefined,
        }),
      )?.cadence,
    ).toEqual({ kind: "one_shot" });
    expect(toUpcomingReminderRow(definition({ cadence: undefined }))).toBeNull();
    expect(toUpcomingReminderRow(definition({ scheduleKind: "future_kind" }))).toBeNull();
    expect(toUpcomingReminderRow(definition({ nextFireAt: undefined }))).toBeNull();
  });
});
