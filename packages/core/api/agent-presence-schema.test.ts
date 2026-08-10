import { describe, expect, it } from "vitest";
import { parseWithFallback } from "./schema";
import {
  AgentPresenceResponseSchema,
  EMPTY_AGENT_PRESENCE_RESPONSE,
} from "./schemas";

describe("AgentPresenceResponseSchema", () => {
  it("accepts the binary roster and ignores future fields", () => {
    const parsed = AgentPresenceResponseSchema.parse({
      items: [
        { agent_id: "agent-1", presence: "online", future: true },
        { agent_id: "agent-2", presence: "offline" },
      ],
      future_envelope: "ignored",
    });
    expect(parsed.items).toEqual([
      { agent_id: "agent-1", presence: "online", future: true },
      { agent_id: "agent-2", presence: "offline" },
    ]);
  });

  it.each([
    ["missing items", {}],
    ["null items", { items: null }],
    ["missing presence", { items: [{ agent_id: "agent-1" }] }],
    ["future enum", { items: [{ agent_id: "agent-1", presence: "busy" }] }],
    [
      "duplicate Agent",
      {
        items: [
          { agent_id: "agent-1", presence: "online" },
          { agent_id: "agent-1", presence: "offline" },
        ],
      },
    ],
  ])("fails closed for %s", (_name, raw) => {
    expect(
      parseWithFallback(
        raw,
        AgentPresenceResponseSchema,
        EMPTY_AGENT_PRESENCE_RESPONSE,
        { endpoint: "GET /api/agents/presence" },
      ),
    ).toEqual(EMPTY_AGENT_PRESENCE_RESPONSE);
  });
});
