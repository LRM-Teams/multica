import { describe, expect, it } from "vitest";
import {
  conversationMessageHref,
  findConversationHandles,
  parseConversationHandle,
  splitTextWithConversationHandles,
} from "./handle";

describe("parseConversationHandle", () => {
  it("parses a channel name", () => {
    expect(parseConversationHandle("#general")).toEqual({
      kind: "channel",
      name: "general",
      messagePrefix: null,
    });
  });

  it("parses a CLI #channel:messageShortId target", () => {
    expect(parseConversationHandle("#raft-research:a291584b")).toEqual({
      kind: "channel",
      name: "raft-research",
      messagePrefix: "a291584b",
    });
  });

  it("parses a DM handle and optional message prefix", () => {
    expect(parseConversationHandle("dm:@alice")).toEqual({
      kind: "dm",
      name: "alice",
      messagePrefix: null,
    });
    expect(parseConversationHandle("dm:@alice:a291584b")).toEqual({
      kind: "dm",
      name: "alice",
      messagePrefix: "a291584b",
    });
  });

  it("rejects extra colons and non-hex prefixes", () => {
    expect(parseConversationHandle("#chan:abc:def")).toBeNull();
    expect(parseConversationHandle("#chan:zzzz")).toBeNull();
    expect(parseConversationHandle("general")).toBeNull();
  });
});

describe("findConversationHandles / splitTextWithConversationHandles", () => {
  it("lifts the handle out of a target line", () => {
    expect(splitTextWithConversationHandles("target: #raft-research:a291584b")).toEqual([
      { kind: "text", value: "target: " },
      { kind: "handle", value: "#raft-research:a291584b" },
    ]);
  });

  it("finds a bare channel handle", () => {
    expect(findConversationHandles("#general").map((hit) => hit.raw)).toEqual(["#general"]);
  });

  it("does not steal a handle glued to a preceding word", () => {
    expect(findConversationHandles("see#general")).toEqual([]);
  });
});

describe("conversationMessageHref", () => {
  it("appends thread then message, matching reminder/notification deep links", () => {
    expect(conversationMessageHref("/acme/channels/chan-1")).toBe("/acme/channels/chan-1");
    expect(
      conversationMessageHref("/acme/channels/chan-1", { messageId: "msg-1" }),
    ).toBe("/acme/channels/chan-1?message=msg-1");
    expect(
      conversationMessageHref("/acme/channels/chan-1", {
        threadId: "root-1",
        messageId: "reply-2",
      }),
    ).toBe("/acme/channels/chan-1?thread=root-1&message=reply-2");
  });
});
