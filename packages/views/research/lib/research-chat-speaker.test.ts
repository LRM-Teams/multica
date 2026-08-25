import { describe, expect, it } from "vitest";
import type { ResearchPresenceMap } from "@multica/core/research";
import type { ResearchMessage } from "@multica/core/types";
import { researchChatSpeakerForMessage } from "./research-chat-speaker";

function message(overrides: Partial<ResearchMessage> = {}): ResearchMessage {
  return {
    id: "message-1",
    session_id: "session-1",
    sender_type: "agent",
    sender_id: "agent-1",
    target_agent_id: null,
    body: "完成调研。",
    created_at: "2026-08-25T08:00:00Z",
    ...overrides,
  };
}

function presence(): ResearchPresenceMap {
  return {
    "agent-1": {
      activity: "已完成当前任务",
      updatedAt: 1,
      phase: "done",
      role: "member",
      name: "OpenAI 趋势研究员",
      avatarUrl: null,
      fleetMemberId: null,
      taskId: null,
      nodeId: null,
      branchId: null,
      stage: null,
      expiresAt: null,
      staleReason: null,
    },
  };
}

describe("researchChatSpeakerForMessage", () => {
  it("resolves a V6 run-scoped Agent from presence without a legacy Fleet member", () => {
    expect(researchChatSpeakerForMessage(message(), [], presence())).toEqual({
      agentId: "agent-1",
      name: "OpenAI 趋势研究员",
      role: "member",
    });
  });

  it("resolves the actor attached to a system-authored process event", () => {
    expect(
      researchChatSpeakerForMessage(
        message({
          sender_type: "system",
          sender_id: null,
          meta: { actor_agent_id: "agent-1" },
        }),
        [],
        presence(),
      ),
    ).toMatchObject({ agentId: "agent-1", name: "OpenAI 趋势研究员" });
  });

  it("does not mistake the user message target for its speaker", () => {
    expect(
      researchChatSpeakerForMessage(
        message({
          sender_type: "user",
          sender_id: "user-1",
          target_agent_id: "agent-1",
        }),
        [],
        presence(),
      ),
    ).toBeNull();
  });
});
