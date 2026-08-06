// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ActivityEvent } from "./activity-event";
import { projectLatestActivity, upsertActivityEvents } from "./activity-event-reducer";

function evt(id: string, occurred_at: string, text = id): ActivityEvent {
  return {
    id,
    occurred_at,
    text,
    agent_id: "agent-1",
    activity_kind: "text",
    detail_kind: "text",
    target_ref: { kind: "agent", id: "agent-1" },
  };
}

describe("upsertActivityEvents", () => {
  it("adds new events ordered chronologically by occurred_at", () => {
    const result = upsertActivityEvents(
      [evt("b", "2026-07-10T10:01:00Z")],
      [evt("c", "2026-07-10T10:02:00Z"), evt("a", "2026-07-10T10:00:00Z")],
    );
    expect(result.map((e) => e.id)).toEqual(["a", "b", "c"]);
  });

  it("replaces an existing id with the fresher event (BE re-sent a bigger aggregate)", () => {
    const first = upsertActivityEvents([], evt("t1", "2026-07-10T10:00:00Z", "Thinking…"));
    const grown = upsertActivityEvents(first, evt("t1", "2026-07-10T10:00:00Z", "Thinking about the fix…"));
    expect(grown).toHaveLength(1);
    expect(grown[0]?.text).toBe("Thinking about the fix…");
  });

  it("is idempotent — re-applying the same event yields an equal list", () => {
    const e = evt("x", "2026-07-10T10:00:00Z");
    const once = upsertActivityEvents([], e);
    const twice = upsertActivityEvents(once, e);
    expect(twice).toEqual(once);
  });

  it("settles a batch carrying two rows for one id on the last (delta then coalesced)", () => {
    const result = upsertActivityEvents(
      [],
      [evt("t1", "2026-07-10T10:00:00Z", "delta"), evt("t1", "2026-07-10T10:00:00Z", "coalesced")],
    );
    expect(result).toHaveLength(1);
    expect(result[0]?.text).toBe("coalesced");
  });

  it("does not mutate the input array", () => {
    const current = [evt("a", "2026-07-10T10:00:00Z")];
    upsertActivityEvents(current, evt("b", "2026-07-10T10:01:00Z"));
    expect(current).toHaveLength(1);
  });

  it("normalizes order even when incoming is empty (the #500 empty-liveEvents regression)", () => {
    // The hook computes `upsertActivityEvents(data, liveEvents)`; before any WS
    // event lands `liveEvents === []`, and `data` is the REST page in wire order
    // (DESC). The output MUST still be chronological ASC — otherwise the timeline
    // renders newest-on-top, the compact card `slice(-5)` takes the OLDEST five,
    // and `projectLatestActivity` (last element) reports the OLDEST as "latest".
    const descFromRest = [
      evt("c", "2026-07-10T10:02:00Z"),
      evt("b", "2026-07-10T10:01:00Z"),
      evt("a", "2026-07-10T10:00:00Z"),
    ];
    const result = upsertActivityEvents(descFromRest, []);
    expect(result.map((e) => e.id)).toEqual(["a", "b", "c"]);
    expect(projectLatestActivity(result)?.id).toBe("c");
  });
});

describe("projectLatestActivity", () => {
  it("returns the most recent event for the header/hover latest-state read", () => {
    const events = upsertActivityEvents(
      [],
      [evt("a", "2026-07-10T10:00:00Z"), evt("b", "2026-07-10T10:05:00Z")],
    );
    expect(projectLatestActivity(events)?.id).toBe("b");
  });

  it("returns null for an empty stream", () => {
    expect(projectLatestActivity([])).toBeNull();
  });
});
