import { describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

describe("LRM-581 chat inbox cancel API (client)", () => {
  it("POSTs Stop to chat session agent-inbox/events/{id}/cancel", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          ok: true,
          inbox_event_id: "event-1",
          agent_id: "agent-1",
          status: "cancelled",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    const result = await client.cancelChatInboxEvent("session-1", "event-1");

    expect(fetchMock.mock.calls[0]).toMatchObject([
      "https://api.example.test/api/chat/sessions/session-1/agent-inbox/events/event-1/cancel",
      { method: "POST" },
    ]);
    expect(result).toEqual({
      ok: true,
      inbox_event_id: "event-1",
      agent_id: "agent-1",
      status: "cancelled",
    });
  });
});
