import { describe, expect, it, vi, beforeEach } from "vitest";
import { ApiClient } from "./client";

/**
 * #832 — asserts the REQUEST, not that a method ran.
 *
 * Felix caught this in review: transfer was sending `{role:"owner"}` to the
 * member-role PATCH, which the server rejects outright (channel.go:1761) in
 * favour of a dedicated POST route. Every transfer would have 400'd. The views
 * suite could not see it because the api client is mocked there and a mock
 * accepts any arguments — both halves self-consistent, only the seam wrong.
 *
 * So these assert path and verb against a stubbed fetch: the only shape of test
 * that can catch "we called the right function with the wrong request".
 */
describe("role-change requests (#832)", () => {
  let fetchMock: ReturnType<typeof vi.fn>;
  let client: ApiClient;

  beforeEach(() => {
    fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ role: "manager" }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    client = new ApiClient({ baseUrl: "" });
  });

  it("transfer uses the dedicated POST route — NOT role:'owner' on the PATCH", async () => {
    await client.transferChannelOwnership("chan-1", "user", "user-2");
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toContain(
      "/api/channels/chan-1/members/user/user-2/transfer-ownership",
    );
    expect(init?.method).toBe("POST");
    // The exact value the server refuses must never appear on this path.
    expect(String(init?.body ?? "")).not.toContain('"owner"');
  });

  it("promote/demote use PATCH on the member-role route with the target role", async () => {
    await client.updateChannelMemberRole("chan-1", "agent", "agent-9", "manager");
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toContain("/api/channels/chan-1/members/agent/agent-9");
    expect(String(url)).not.toContain("transfer-ownership");
    expect(init?.method).toBe("PATCH");
    expect(String(init?.body)).toContain('"manager"');
  });
});
