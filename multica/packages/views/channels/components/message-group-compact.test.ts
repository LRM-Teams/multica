// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import {
  MESSAGE_GROUP_MAX_GAP_MS,
  buildMessageGroupCompactMap,
  isGroupableChannelMessage,
  shouldStartMessageGroup,
} from "./message-group-compact";

function makeMessage(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    id: "msg-1",
    channel_id: "ch-1",
    workspace_id: "ws-1",
    seq: 1,
    type: "user",
    author_id: "user-a",
    author_name: "Alice",
    content: "hello",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-22T03:28:00.000Z",
    ...overrides,
  };
}

describe("isGroupableChannelMessage", () => {
  it("rejects system and deleted rows", () => {
    expect(isGroupableChannelMessage(makeMessage())).toBe(true);
    expect(isGroupableChannelMessage(makeMessage({ type: "system" }))).toBe(false);
    expect(isGroupableChannelMessage(makeMessage({ deleted_at: "2026-07-22T03:30:00.000Z" }))).toBe(
      false,
    );
  });
});

describe("shouldStartMessageGroup", () => {
  const tz = "UTC";

  it("starts a group for the first row or when the author changes", () => {
    const first = makeMessage({ id: "m1", created_at: "2026-07-22T03:28:00.000Z" });
    const sameAuthor = makeMessage({
      id: "m2",
      created_at: "2026-07-22T03:29:00.000Z",
    });
    const otherAuthor = makeMessage({
      id: "m3",
      author_id: "user-b",
      created_at: "2026-07-22T03:29:30.000Z",
    });

    expect(shouldStartMessageGroup(null, first, { tz })).toBe(true);
    expect(shouldStartMessageGroup(first, sameAuthor, { tz })).toBe(false);
    expect(shouldStartMessageGroup(sameAuthor, otherAuthor, { tz })).toBe(true);
  });

  it("breaks when the gap exceeds five minutes", () => {
    const prev = makeMessage({ id: "m1", created_at: "2026-07-22T03:28:00.000Z" });
    const within = makeMessage({ id: "m2", created_at: "2026-07-22T03:32:59.000Z" });
    const after = makeMessage({
      id: "m3",
      created_at: "2026-07-22T03:33:01.000Z",
    });

    expect(shouldStartMessageGroup(prev, within, { tz, maxGapMs: MESSAGE_GROUP_MAX_GAP_MS })).toBe(
      false,
    );
    expect(shouldStartMessageGroup(prev, after, { tz, maxGapMs: MESSAGE_GROUP_MAX_GAP_MS })).toBe(
      true,
    );
  });

  it("breaks across local calendar days", () => {
    const prev = makeMessage({ id: "m1", created_at: "2026-07-22T23:59:00.000Z" });
    const nextDay = makeMessage({ id: "m2", created_at: "2026-07-23T00:01:00.000Z" });

    expect(shouldStartMessageGroup(prev, nextDay, { tz })).toBe(true);
  });

  it("breaks after system messages and date dividers", () => {
    const prev = makeMessage({ id: "m1", created_at: "2026-07-22T03:28:00.000Z" });
    const system = makeMessage({
      id: "sys",
      type: "system",
      author_id: null,
      created_at: "2026-07-22T03:28:30.000Z",
    });
    const afterSystem = makeMessage({ id: "m2", created_at: "2026-07-22T03:29:00.000Z" });

    expect(shouldStartMessageGroup(prev, afterSystem, { tz, hasDateDivider: true })).toBe(true);
    expect(shouldStartMessageGroup(system, afterSystem, { tz })).toBe(true);
    expect(shouldStartMessageGroup(prev, system, { tz })).toBe(true);
  });
});

describe("buildMessageGroupCompactMap", () => {
  const tz = "UTC";

  it("marks consecutive same-author rows compact and skips folded rows", () => {
    const lead = makeMessage({ id: "m1", created_at: "2026-07-22T03:28:00.000Z" });
    const folded = makeMessage({
      id: "folded",
      type: "system",
      author_id: null,
      created_at: "2026-07-22T03:28:10.000Z",
    });
    const compact = makeMessage({ id: "m2", created_at: "2026-07-22T03:29:00.000Z" });
    const nextLead = makeMessage({
      id: "m3",
      author_id: "user-b",
      created_at: "2026-07-22T03:29:30.000Z",
    });

    const map = buildMessageGroupCompactMap([lead, folded, compact, nextLead], {
      foldedIds: new Set(["folded"]),
      tz,
    });

    expect(map.get("m1")).toBe(false);
    expect(map.get("m2")).toBe(true);
    expect(map.get("m3")).toBe(false);
    expect(map.has("folded")).toBe(false);
  });
});
