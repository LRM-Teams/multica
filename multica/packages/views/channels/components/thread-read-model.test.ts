// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { mapThreadParticipants } from "./thread-read-model";

function root(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    id: "root",
    channel_id: "c1",
    workspace_id: "w1",
    seq: 1,
    type: "user",
    author_id: "user-a",
    author_name: "Ann",
    content: "Root",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-04T09:00:00Z",
    ...overrides,
  };
}

describe("mapThreadParticipants (#251)", () => {
  it("maps the BE participant list to chips, normalizing member_type and identity", () => {
    const participants = mapThreadParticipants(
      root({
        thread_participants: [
          { key: "user:user-a", member_type: "user", member_id: "user-a", name: "ann", display_name: "Ann", followed: false },
          { key: "agent:agent-c", member_type: "agent", member_id: "agent-c", name: "cy", display_name: "Cy", followed: true },
        ],
      }),
    );

    expect(participants).toHaveLength(2);
    expect(participants[0]).toMatchObject({ key: "user:user-a", memberType: "user", memberId: "user-a", displayName: "Ann" });
    expect(participants[1]).toMatchObject({ key: "agent:agent-c", memberType: "agent", memberId: "agent-c", displayName: "Cy" });
  });

  it("returns empty when the BE sent no participants (caller falls back structurally)", () => {
    expect(mapThreadParticipants(root())).toEqual([]);
    expect(mapThreadParticipants(root({ thread_participants: [] }))).toEqual([]);
  });

  it("skips an entry without a member_id and derives a key/name when absent", () => {
    const participants = mapThreadParticipants(
      root({
        thread_participants: [
          { key: "", member_type: "agent", member_id: "agent-x", name: "", display_name: "", followed: false },
          { key: "", member_type: "user", member_id: "", name: "", display_name: "", followed: false },
        ],
      }),
    );

    expect(participants).toHaveLength(1);
    expect(participants[0]).toMatchObject({ key: "agent:agent-x", memberType: "agent", displayName: "agent-x" });
  });
});
