import { describe, expect, it } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import {
  areChannelMessageBubblePropsEqual,
  areMessageBodyPropsEqual,
  channelMessageRenderEqual,
} from "./channel-message-render-equality";

function baseMessage(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    id: "msg-1",
    channel_id: "ch-1",
    workspace_id: "ws-1",
    seq: 1,
    type: "user",
    author_id: "user-1",
    author_name: "Alice",
    content: "hello",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-22T00:00:00.000Z",
    ...overrides,
  };
}

describe("channelMessageRenderEqual", () => {
  it("treats identical messages as equal", () => {
    const message = baseMessage();
    expect(channelMessageRenderEqual(message, { ...message })).toBe(true);
  });

  it("detects content changes", () => {
    const prev = baseMessage();
    const next = baseMessage({ content: "updated" });
    expect(channelMessageRenderEqual(prev, next)).toBe(false);
  });

  it("detects reaction changes", () => {
    const prev = baseMessage({ reactions: [{ emoji: "👍", count: 1, user_ids: ["u1"] }] });
    const next = baseMessage({ reactions: [{ emoji: "👍", count: 2, user_ids: ["u1", "u2"] }] });
    expect(channelMessageRenderEqual(prev, next)).toBe(false);
  });
});

describe("areChannelMessageBubblePropsEqual", () => {
  it("skips re-render when only unrelated bubble flags change", () => {
    const message = baseMessage();
    const prev = {
      message,
      currentUserId: "user-1",
      highlighted: false,
      compact: false,
    };
    const next = {
      message: { ...message },
      currentUserId: "user-1",
      highlighted: false,
      compact: false,
    };
    expect(areChannelMessageBubblePropsEqual(prev, next)).toBe(true);
  });

  it("re-renders when highlight toggles", () => {
    const message = baseMessage();
    const base = {
      message,
      currentUserId: "user-1",
    };
    expect(
      areChannelMessageBubblePropsEqual(
        { ...base, highlighted: false },
        { ...base, highlighted: true },
      ),
    ).toBe(false);
  });
});

describe("areMessageBodyPropsEqual", () => {
  it("treats identical body props as equal", () => {
    const props = {
      content: "hello",
      parts: [{ type: "text" as const, text: "hello" }],
      compact: false,
    };
    expect(areMessageBodyPropsEqual(props, { ...props })).toBe(true);
  });

  it("detects highlight query changes", () => {
    const base = { content: "hello world" };
    expect(
      areMessageBodyPropsEqual(
        { ...base, highlightQuery: "hello" },
        { ...base, highlightQuery: "world" },
      ),
    ).toBe(false);
  });
});
