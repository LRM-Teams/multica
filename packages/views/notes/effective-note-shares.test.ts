import { describe, expect, it } from "vitest";
import type { NotePage } from "@multica/core/types";
import { effectiveNoteShareIds } from "./effective-note-shares";

function page(overrides: Partial<NotePage> = {}): NotePage {
  return {
    id: "page-1",
    workspace_id: "ws-1",
    parent_id: null,
    owner_user_id: "owner-1",
    title: "Note",
    content: "",
    sort_key: "a",
    share_user_ids: [],
    can_manage_shares: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    deleted_at: null,
    ...overrides,
  };
}

describe("effectiveNoteShareIds", () => {
  it("inherits member and channel shares from ancestor pages", () => {
    const parent = page({
      id: "parent",
      share_user_ids: ["user-2"],
      share_channel_ids: ["channel-1"],
    });
    const child = page({ id: "child", parent_id: "parent" });

    expect(effectiveNoteShareIds([parent, child], child)).toEqual({
      shareUserIds: ["user-2"],
      shareAgentIds: [],
      shareChannelIds: ["channel-1"],
    });
  });

  it("keeps the child page's own shares first and does not inherit agent shares", () => {
    const parent = page({
      id: "parent",
      share_user_ids: ["user-2"],
      share_agent_ids: ["agent-parent"],
      share_channel_ids: ["channel-1"],
    });
    const child = page({
      id: "child",
      parent_id: "parent",
      share_user_ids: ["user-3"],
      share_agent_ids: ["agent-child"],
    });

    expect(effectiveNoteShareIds([parent, child], child)).toEqual({
      shareUserIds: ["user-3", "user-2"],
      shareAgentIds: ["agent-child"],
      shareChannelIds: ["channel-1"],
    });
  });

  it("treats a child as private when the parent is only shared with an agent", () => {
    const parent = page({ id: "parent", share_agent_ids: ["agent-1"] });
    const child = page({ id: "child", parent_id: "parent" });

    expect(effectiveNoteShareIds([parent, child], child)).toEqual({
      shareUserIds: [],
      shareAgentIds: [],
      shareChannelIds: [],
    });
  });
});
