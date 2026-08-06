// @vitest-environment node
import { describe, expect, it } from "vitest";
import { isConversationMuted, sumUnmutedUnreadCounts } from "./conversation-muted";
import { isTypingActorVisible } from "./conversation-typing";

describe("isConversationMuted", () => {
  it("returns true when muted_at is set", () => {
    expect(isConversationMuted({ muted_at: "2026-01-01T00:00:00Z" })).toBe(true);
  });

  it("returns true when muted flag is true", () => {
    expect(isConversationMuted({ muted: true })).toBe(true);
  });

  it("returns false when neither signal is present", () => {
    expect(isConversationMuted({})).toBe(false);
    expect(isConversationMuted({ muted_at: null, muted: false })).toBe(false);
  });
});

describe("isTypingActorVisible", () => {
  // Anchor 7 / A8: an offline or working agent surfaces via the Run / working
  // indicator (queue → wake), NEVER as a transient "typing" indicator. Humans
  // are the only actors that legitimately produce a typing pulse.
  it("excludes agents from the typing indicator", () => {
    expect(isTypingActorVisible("agent")).toBe(false);
  });

  it("keeps human / lark / system actors visible", () => {
    expect(isTypingActorVisible("user")).toBe(true);
    expect(isTypingActorVisible("lark")).toBe(true);
    expect(isTypingActorVisible("system")).toBe(true);
  });
});

describe("sumUnmutedUnreadCounts", () => {
  const items = [
    { id: "a", unread: 3, muted: false },
    { id: "b", unread: 5, muted: true },
    { id: "c", unread: 2, muted: false },
  ];

  it("excludes muted conversations from aggregate unread", () => {
    expect(
      sumUnmutedUnreadCounts(
        items,
        (item) => item.unread,
        (item) => item.muted,
      ),
    ).toBe(5);
  });
});
