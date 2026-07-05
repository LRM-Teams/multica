import { describe, expect, it } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { mapThreadParticipants, mapThreadWakeAnnotations } from "./thread-read-model";

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

describe("mapThreadWakeAnnotations (#251 / #196)", () => {
  it("keeps only agent records and coalesces a null reason to undefined", () => {
    const annotations = mapThreadWakeAnnotations(
      root({
        thread_wake_annotations: [
          { key: "agent:agent-c", member_type: "agent", member_id: "agent-c", display_name: "Cy", state: "no_reply", reason: null },
          // A human is never woken — it must not survive the mapping.
          { key: "user:user-a", member_type: "user", member_id: "user-a", display_name: "Ann", state: "pending" },
        ],
      }),
    );

    expect(annotations).toHaveLength(1);
    expect(annotations[0]).toMatchObject({ key: "agent:agent-c", memberType: "agent", state: "no_reply" });
    expect(annotations[0]?.reason).toBeUndefined();
  });

  it("passes an unknown/future state through untouched (the strip owns dropping it)", () => {
    const annotations = mapThreadWakeAnnotations(
      root({
        thread_wake_annotations: [
          { key: "agent:agent-z", member_type: "agent", member_id: "agent-z", display_name: "Zed", state: "escalated" },
        ],
      }),
    );

    expect(annotations).toHaveLength(1);
    expect(annotations[0]?.state).toBe("escalated");
  });

  it("returns empty when the BE sent no annotations", () => {
    expect(mapThreadWakeAnnotations(root())).toEqual([]);
  });
});
