import { describe, expect, it } from "vitest";
import type { MemberWithUser } from "@multica/core/types";
import { buildNoteShareNames } from "./share-labels";

function member(overrides: Partial<MemberWithUser>): MemberWithUser {
  return {
    id: "member-1",
    workspace_id: "ws-a",
    user_id: "user-1",
    role: "member",
    created_at: "2026-01-01T00:00:00Z",
    name: "user1",
    display_name: "User One",
    email: "user1@example.com",
    avatar_url: null,
    description: "",
    ...overrides,
  };
}

describe("buildNoteShareNames", () => {
  it("resolves share names from the note workspace and appends the workspace name", () => {
    const membersByUserId = new Map([
      ["user-zhang", member({ user_id: "user-zhang", display_name: "Zhang San" })],
      ["user-li", member({ user_id: "user-li", display_name: "Li Si" })],
    ]);

    expect(
      buildNoteShareNames({
        shareUserIds: ["user-zhang", "user-li"],
        membersByUserId,
        workspaceName: "LRM-team",
        unknownMemberLabel: "Unknown member",
        formatName: (name, workspace) => `${name} (${workspace})`,
      }),
    ).toEqual(["Zhang San (LRM-team)", "Li Si (LRM-team)"]);
  });

  it("does not expose raw user ids while the member directory is still loading", () => {
    expect(
      buildNoteShareNames({
        shareUserIds: ["user-li"],
        membersByUserId: new Map(),
        workspaceName: "LRM-team",
        unknownMemberLabel: "Unknown member",
        formatName: (name, workspace) => `${name} (${workspace})`,
      }),
    ).toEqual(["Unknown member (LRM-team)"]);
  });

  it("appends shared agent and channel names after member names", () => {
    expect(
      buildNoteShareNames({
        shareUserIds: ["user-zhang"],
        membersByUserId: new Map([
          ["user-zhang", member({ user_id: "user-zhang", display_name: "Zhang San" })],
        ]),
        shareAgentIds: ["agent-1"],
        agentsById: new Map([["agent-1", { name: "notes-bot", display_name: "笔记助手" }]]),
        shareChannelIds: ["ch-1"],
        channelsById: new Map([["ch-1", { name: "sprint-room" }]]),
        workspaceName: "LRM-team",
        unknownMemberLabel: "Unknown member",
        formatName: (name, workspace) => `${name} (${workspace})`,
      }),
    ).toEqual(["Zhang San (LRM-team)", "笔记助手", "sprint-room"]);
  });
});
