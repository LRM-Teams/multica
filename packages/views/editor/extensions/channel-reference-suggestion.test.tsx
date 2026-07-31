import { describe, it, expect, vi } from "vitest";
import type { QueryClient } from "@tanstack/react-query";
import { channelKeys } from "@multica/core/channels/queries";
import type { Channel } from "@multica/core/types";
import type { MentionItem } from "./mention-suggestion";
import { createChannelReferenceSuggestion } from "./channel-reference-suggestion";

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => "ws-1",
}));

function fakeChannel(overrides: Partial<Channel>): Channel {
  return {
    id: "c1",
    workspace_id: "ws-1",
    name: "general",
    kind: "group",
    description: null,
    lark_chat_id: null,
    created_by: "u1",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function fakeQc(channels: Channel[]): QueryClient {
  const map = new Map<string, unknown>();
  map.set(JSON.stringify(channelKeys.list("ws-1")), channels);
  return {
    getQueryData: (key: readonly unknown[]) => map.get(JSON.stringify(key)),
  } as unknown as QueryClient;
}

describe("createChannelReferenceSuggestion", () => {
  it("returns cached group channels matching the query, filtering out DMs and archived channels", () => {
    const qc = fakeQc([
      fakeChannel({ id: "c1", name: "general" }),
      fakeChannel({ id: "c2", name: "engineering", description: "eng talk" }),
      // DM channels are never a valid `#` reference target.
      fakeChannel({ id: "c3", name: "dm-alice-bob", kind: "dm" }),
      // Archived groups shouldn't surface as insertable references either.
      fakeChannel({ id: "c4", name: "old-project", archived_at: "2026-01-02T00:00:00Z" }),
    ]);

    const config = createChannelReferenceSuggestion(qc);
    const items = config.items!({ query: "eng", editor: {} as never }) as MentionItem[];

    expect(items).toEqual([
      expect.objectContaining({ id: "c2", label: "engineering", type: "channel" }),
    ]);
  });

  it("returns every eligible channel when the query is empty", () => {
    const qc = fakeQc([
      fakeChannel({ id: "c1", name: "general" }),
      fakeChannel({ id: "c2", name: "engineering" }),
    ]);

    const config = createChannelReferenceSuggestion(qc);
    const items = config.items!({ query: "", editor: {} as never }) as MentionItem[];

    expect(items.map((i) => i.id)).toEqual(["c1", "c2"]);
  });

  it("inserts a channelReference node with the channel's id and name on select", () => {
    const qc = fakeQc([fakeChannel({ id: "c1", name: "general" })]);
    const config = createChannelReferenceSuggestion(qc);

    const insertContentAt = vi.fn().mockReturnThis();
    const focus = vi.fn().mockReturnThis();
    const run = vi.fn();
    const chain = vi.fn(() => ({ focus, insertContentAt, run }));
    focus.mockReturnValue({ insertContentAt });
    insertContentAt.mockReturnValue({ run });
    const editor = { chain } as never;

    config.command!({
      editor,
      range: { from: 0, to: 1 },
      props: { id: "c1", label: "general" } as MentionItem,
    } as never);

    expect(insertContentAt).toHaveBeenCalledWith(
      { from: 0, to: 1 },
      { type: "channelReference", attrs: { id: "c1", label: "general" } },
    );
  });
});
