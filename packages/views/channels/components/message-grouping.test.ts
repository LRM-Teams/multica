import { describe, expect, it } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import {
  MESSAGE_GROUP_WINDOW_MS,
  computeCompactMessageIds,
  isCompactContinuation,
} from "./message-grouping";

function msg(overrides: Partial<ChannelMessage> & { id: string }): ChannelMessage {
  return {
    channel_id: "c1",
    workspace_id: "w1",
    seq: 1,
    type: "user",
    author_id: "user-frank",
    author_name: "Frank An",
    content: "hi",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-22T03:28:00Z",
    ...overrides,
  };
}

describe("isCompactContinuation", () => {
  it("groups same author within the 5-minute window (Frank 11:28→11:29)", () => {
    const lead = msg({ id: "m1", created_at: "2026-07-22T03:28:00Z", content: "858 pr怎么还是raft？" });
    const follow = msg({ id: "m2", created_at: "2026-07-22T03:29:00Z", content: "draft？" });
    expect(isCompactContinuation(lead, follow)).toBe(true);
  });

  it("breaks when the gap exceeds 5 minutes", () => {
    const lead = msg({ id: "m1", created_at: "2026-07-22T03:28:00Z" });
    const follow = msg({
      id: "m2",
      created_at: new Date(Date.parse(lead.created_at) + MESSAGE_GROUP_WINDOW_MS + 1).toISOString(),
    });
    expect(isCompactContinuation(lead, follow)).toBe(false);
  });

  it("breaks on author change", () => {
    const lead = msg({ id: "m1", author_id: "user-a" });
    const follow = msg({ id: "m2", author_id: "user-b", created_at: "2026-07-22T03:28:30Z" });
    expect(isCompactContinuation(lead, follow)).toBe(false);
  });

  it("breaks on system messages (either side)", () => {
    const user = msg({ id: "m1" });
    const system = msg({
      id: "m2",
      type: "system",
      author_id: null,
      author_name: "System",
      created_at: "2026-07-22T03:28:10Z",
    });
    const next = msg({ id: "m3", created_at: "2026-07-22T03:28:20Z" });
    expect(isCompactContinuation(user, system)).toBe(false);
    expect(isCompactContinuation(system, next)).toBe(false);
  });

  it("breaks on local-day boundary flag", () => {
    const lead = msg({ id: "m1", created_at: "2026-07-22T15:58:00Z" });
    const follow = msg({ id: "m2", created_at: "2026-07-22T16:01:00Z" });
    expect(isCompactContinuation(lead, follow, { startsNewDay: true })).toBe(false);
  });

  it("does not compact the first message", () => {
    expect(isCompactContinuation(null, msg({ id: "m1" }))).toBe(false);
  });
});

describe("computeCompactMessageIds", () => {
  it("marks only continuations, not the lead", () => {
    const messages = [
      msg({ id: "m1", created_at: "2026-07-22T03:28:00Z", content: "858 pr怎么还是raft？" }),
      msg({ id: "m2", created_at: "2026-07-22T03:29:00Z", content: "draft？" }),
      msg({
        id: "m3",
        author_id: "user-other",
        author_name: "Other",
        created_at: "2026-07-22T03:29:30Z",
        content: "ok",
      }),
    ];
    expect([...computeCompactMessageIds(messages)]).toEqual(["m2"]);
  });

  it("respects day-divider ids as hard breaks", () => {
    const messages = [
      msg({ id: "m1", created_at: "2026-07-21T03:28:00Z" }),
      msg({ id: "m2", created_at: "2026-07-22T03:28:30Z" }),
    ];
    expect(computeCompactMessageIds(messages, { dayDividerIds: new Set(["m2"]) }).has("m2")).toBe(
      false,
    );
  });
});
