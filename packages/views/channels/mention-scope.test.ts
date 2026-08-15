// @vitest-environment node
import { describe, it, expect } from "vitest";
import {
  buildGroupMentionAllowedActorIds,
  inviteableUndeliveredMentions,
} from "./mention-scope";

describe("buildGroupMentionAllowedActorIds", () => {
  it("unions workspace users, agents, and channel members", () => {
    const set = buildGroupMentionAllowedActorIds({
      workspaceUserIds: ["u1", "u2"],
      workspaceAgentIds: ["a1"],
      channelMemberIds: ["a2", "u1"],
      viewerUserId: null,
    });
    expect(set.has("u1")).toBe(true);
    expect(set.has("u2")).toBe(true);
    expect(set.has("a1")).toBe(true);
    expect(set.has("a2")).toBe(true);
    expect(set.size).toBe(4);
  });

  it("drops the viewing human from the group @ picker", () => {
    const set = buildGroupMentionAllowedActorIds({
      workspaceUserIds: ["u1", "u2"],
      workspaceAgentIds: ["a1"],
      channelMemberIds: ["a2", "u1"],
      viewerUserId: "u1",
    });
    expect(set.has("u1")).toBe(false);
    expect(set.has("u2")).toBe(true);
    expect(set.has("a1")).toBe(true);
    expect(set.has("a2")).toBe(true);
    expect(set.size).toBe(3);
  });
});

describe("inviteableUndeliveredMentions", () => {
  it("keeps only invite actions and maps member→user", () => {
    expect(
      inviteableUndeliveredMentions([
        {
          type: "member",
          id: "u9",
          label: "Alice",
          actions: ["invite"],
        },
        {
          type: "agent",
          id: "a9",
          handle: "bob",
          actions: ["invite"],
        },
        {
          type: "member",
          id: "u8",
          actions: ["notify"],
        },
      ]),
    ).toEqual([
      { member_type: "user", member_id: "u9", display: "Alice" },
      { member_type: "agent", member_id: "a9", display: "bob" },
    ]);
  });

  it("returns empty for missing/empty", () => {
    expect(inviteableUndeliveredMentions(undefined)).toEqual([]);
    expect(inviteableUndeliveredMentions([])).toEqual([]);
  });
});
