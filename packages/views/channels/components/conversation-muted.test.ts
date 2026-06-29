import { describe, expect, it } from "vitest";
import { isConversationMuted, sumUnmutedUnreadCounts } from "./conversation-muted";

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
