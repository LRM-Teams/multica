// @vitest-environment node
import { describe, it, expect } from "vitest";
import type { DMItem } from "@multica/core/dm";
import type { Channel } from "@multica/core/types";
import { buildPinnedConversationEntries } from "./pinned-conversations";

function makeDm(overrides: Partial<DMItem> = {}): DMItem {
  return {
    id: "dm-1",
    source: "dm_channel",
    peer: { type: "user", id: "peer-1", name: "Alice" },
    unread: 0,
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  };
}

function makeChannel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: "ch-1",
    workspace_id: "ws-1",
    name: "general",
    kind: "group",
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  };
}

describe("buildPinnedConversationEntries", () => {
  it("returns only pinned DMs and channels in a unified list", () => {
    const entries = buildPinnedConversationEntries(
      [
        makeDm({ id: "dm-pinned", pinned_at: "2026-07-03T10:00:00Z", peer: { type: "user", id: "p1", name: "Alice" } }),
        makeDm({ id: "dm-free" }),
      ],
      [
        makeChannel({ id: "ch-pinned", pinned_at: "2026-07-03T12:00:00Z", name: "eng" }),
        makeChannel({ id: "ch-free", name: "random" }),
      ],
    );

    expect(entries.map((e) => e.id)).toEqual(["ch-pinned", "dm-pinned"]);
    expect(entries[0]?.kind).toBe("channel");
    expect(entries[1]?.kind).toBe("dm");
  });

  it("orders by pinned_at descending (most recently pinned first)", () => {
    const entries = buildPinnedConversationEntries(
      [
        makeDm({ id: "dm-old", pinned_at: "2026-07-01T00:00:00Z" }),
        makeDm({ id: "dm-new", pinned_at: "2026-07-05T00:00:00Z", peer: { type: "user", id: "p2", name: "Bob" } }),
      ],
      [makeChannel({ id: "ch-mid", pinned_at: "2026-07-03T00:00:00Z" })],
    );

    expect(entries.map((e) => e.id)).toEqual(["dm-new", "ch-mid", "dm-old"]);
  });

  it("returns empty when nothing is pinned", () => {
    expect(buildPinnedConversationEntries([makeDm()], [makeChannel()])).toEqual([]);
  });
});
