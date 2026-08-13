import { describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

describe("standalone chat cancel API", () => {
  it("POSTs Stop to chat session cancel without an inbox event", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ ok: true, pending: false }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    const result = await client.cancelStandaloneChat("session-1");

    expect(fetchMock.mock.calls[0]).toMatchObject([
      "https://api.example.test/api/chat/sessions/session-1/cancel",
      { method: "POST" },
    ]);
    expect(result).toEqual({ ok: true, pending: false });
  });
});
