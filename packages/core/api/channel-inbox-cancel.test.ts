import { describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

describe("LRM-425 channel inbox cancel API", () => {
  it("POSTs single cancel to agent-inbox/events/{id}/cancel", async () => {
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
    const result = await client.cancelChannelInboxEvent("channel-1", "event-1");

    expect(fetchMock.mock.calls[0]).toMatchObject([
      "https://api.example.test/api/channels/channel-1/agent-inbox/events/event-1/cancel",
      { method: "POST" },
    ]);
    expect(result).toEqual({
      ok: true,
      inbox_event_id: "event-1",
      agent_id: "agent-1",
      status: "cancelled",
    });
  });

  it("POSTs Stop All once to agent-inbox/cancel-active (no N× fan-out)", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          ok: true,
          cancelled_count: 2,
          cancelled_inbox_event_ids: ["event-a", "event-b"],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    const result = await client.cancelChannelActiveInboxEvents("channel-1");

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0]).toMatchObject([
      "https://api.example.test/api/channels/channel-1/agent-inbox/cancel-active",
      { method: "POST" },
    ]);
    expect(result.cancelled_count).toBe(2);
    expect(result.cancelled_inbox_event_ids).toEqual(["event-a", "event-b"]);
  });
});
