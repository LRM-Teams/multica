import { describe, expect, it } from "vitest";

import { chatTranscriptOptions, isTaskMessageTaskId, taskMessagesOptions } from "./queries";

describe("taskMessagesOptions", () => {
  it("fetches task messages for persisted UUID task ids", () => {
    const taskId = "4a2e8d1c-7f9b-4e2a-9c1d-123456789abc";

    expect(isTaskMessageTaskId(taskId)).toBe(true);
    expect(taskMessagesOptions(taskId).enabled).toBe(true);
  });

  it("does not fetch task messages for optimistic task ids", () => {
    const taskId = "optimistic-optimistic-1778739487737";

    expect(isTaskMessageTaskId(taskId)).toBe(false);
    expect(taskMessagesOptions(taskId).enabled).toBe(false);
  });
});

describe("chatTranscriptOptions (#414 — session-scoped transcript source)", () => {
  const sessionId = "9c1d5efa-1234-4e2a-9c1d-abcdef012345";
  const eventId = "4a2e8d1c-7f9b-4e2a-9c1d-123456789abc";

  it("shares the SAME cache key as taskMessagesOptions so WS seeding is preserved", () => {
    // The whole point of the migration: only the fetch SOURCE moves; the cache
    // key stays keyed on the event/task id, so `task:message` WS seeding keeps
    // the live timeline current with zero WS change.
    expect(chatTranscriptOptions(sessionId, eventId).queryKey).toEqual(
      taskMessagesOptions(eventId).queryKey,
    );
  });

  it("fetches only with a session id AND a persisted UUID event id", () => {
    expect(chatTranscriptOptions(sessionId, eventId).enabled).toBe(true);
    expect(chatTranscriptOptions("", eventId).enabled).toBe(false);
    expect(chatTranscriptOptions(sessionId, "optimistic-1778739487737").enabled).toBe(false);
  });
});
