import { describe, it, expect, vi } from "vitest";
import {
  conversationKeys,
  flattenConversationPages,
  conversationGroupChannels,
  conversationDMs,
} from "./queries";
import type { ConversationListItem, ConversationListResponse } from "./types";

/**
 * LRM-1399 — the unified Conversations module is the single Messages sidebar
 * data source. These tests lock the contract (kind split preserving native
 * channel/DM shapes, global order preserved, cursor contract) that the page
 * depends on, so a regression in the split helpers cannot silently re-drift
 * the two sidebar regions back to separate sources.
 */

function channelItem(id: string, overrides: Partial<ConversationListItem["channel"]> = {}): ConversationListItem {
  return {
    kind: "channel",
    channel: {
      id,
      workspace_id: "w",
      name: `chan-${id}`,
      kind: "group",
      description: null,
      lark_chat_id: null,
      created_by: "u",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      ...overrides,
    },
  };
}

function dmItem(id: string): ConversationListItem {
  return {
    kind: "dm",
    dm: {
      id,
      source: "dm_channel",
      peer: { type: "agent", id: `agent-${id}`, name: `Agent ${id}` },
      unread: 0,
      updated_at: "2026-01-01T00:00:00Z",
    },
  };
}

const emptyInfinite: Parameters<typeof flattenConversationPages>[0] = {
  pages: [],
  pageParams: [],
};

describe("conversationKeys", () => {
  it("namespaces the list under the workspace", () => {
    expect(conversationKeys.all("ws-1")).toEqual(["conversations", "ws-1"]);
    expect(conversationKeys.list("ws-1")).toEqual(["conversations", "ws-1", "list"]);
    expect(conversationKeys.list("ws-1")).not.toEqual(conversationKeys.list("ws-2"));
  });
});

describe("flattenConversationPages", () => {
  it("joins pages in server order", () => {
    const data: Parameters<typeof flattenConversationPages>[0] = {
      pages: [
        { items: [dmItem("a"), channelItem("b")], next_cursor: "c1" },
        { items: [channelItem("c")], next_cursor: undefined },
      ],
      pageParams: [null, "c1"],
    };
    const flat = flattenConversationPages(data);
    expect(flat.map((i) => (i.kind === "dm" ? i.dm!.id : i.channel!.id))).toEqual([
      "a",
      "b",
      "c",
    ]);
  });

  it("returns empty for an empty infinite data", () => {
    expect(flattenConversationPages(emptyInfinite)).toEqual([]);
  });
});

describe("conversationGroupChannels / conversationDMs", () => {
  it("splits a mixed list into native-shaped channel and DM arrays preserving order", () => {
    const items = [dmItem("d1"), channelItem("c1"), channelItem("c2"), dmItem("d2")];

    const channels = conversationGroupChannels(items);
    expect(channels.map((c) => c.id)).toEqual(["c1", "c2"]);
    // Native Channel shape preserved (not a wrapper object).
    expect(channels[0]!.name).toBe("chan-c1");

    const dms = conversationDMs(items);
    expect(dms.map((d) => d.id)).toEqual(["d1", "d2"]);
    // Native DMItem shape preserved — peer + unread fields intact.
    expect(dms[0]!.peer.name).toBe("Agent d1");
    expect(dms[0]!.source).toBe("dm_channel");
  });

  it("ignores kind tags that do not carry a payload", () => {
    const items: ConversationListItem[] = [
      { kind: "channel" },
      dmItem("d1"),
    ];
    expect(conversationGroupChannels(items)).toHaveLength(0);
    expect(conversationDMs(items).map((d) => d.id)).toEqual(["d1"]);
  });
});

describe("conversationsOptions response shape (type contract)", () => {
  it("carries the server cursor so the next page can be fetched", () => {
    // The page flattens pages and reads next_cursor for the infinite query.
    // Pin the response shape against the backend contract.
    const page: ConversationListResponse = {
      items: [channelItem("c1")],
      next_cursor: "opaque",
    };
    expect(page.next_cursor).toBe("opaque");
    expect(page.items[0]!.kind).toBe("channel");
    expect(conversationKeys.list("ws-1").join("/")).toContain("conversations");
  });

  it("invalidateConversations targets the exact conversations list key", async () => {
    const invalidateQueries = vi.fn();
    const qc = { invalidateQueries } as unknown as Parameters<
      typeof import("./queries").invalidateConversations
    >[0];
    const { invalidateConversations } = await import("./queries");
    invalidateConversations(qc, "ws-9");
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: conversationKeys.list("ws-9"),
    });
  });
});
