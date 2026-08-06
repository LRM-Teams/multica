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
 *
 * The URL is asserted with `toBe`, not `toContain`, and that is not style.
 * The first draft of this file constructed `new ApiClient({ baseUrl: "" })` —
 * the constructor takes a string (client.ts:448), so `${this.baseUrl}${path}`
 * produced "[object Object]/api/channels/…". Both cases still passed, because
 * the correct path is a SUBSTRING of the malformed one. A test written to catch
 * a bad request was issuing one and could not see it (Felix, review). `toBe`
 * fails on that URL; `toContain` cannot.
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
    // A string, per the constructor signature. Empty so the asserted URL below
    // is the path itself.
    client = new ApiClient("");
  });

  /** Fails loudly on "no request at all", which an assertion on calls[0] alone
   *  would report as a confusing undefined mismatch. */
  function soleCall() {
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] ?? [];
    return { url: String(url), init: init as RequestInit | undefined };
  }

  it("transfer uses the dedicated POST route — NOT role:'owner' on the PATCH", async () => {
    await client.transferChannelOwnership("chan-1", "user", "user-2");
    const { url, init } = soleCall();
    expect(url).toBe("/api/channels/chan-1/members/user/user-2/transfer-ownership");
    expect(init?.method).toBe("POST");
    // The exact value the server refuses must never appear on this path.
    expect(String(init?.body ?? "")).not.toContain('"owner"');
  });

  it("promote/demote use PATCH on the member-role route with the target role", async () => {
    await client.updateChannelMemberRole("chan-1", "agent", "agent-9", "manager");
    const { url, init } = soleCall();
    expect(url).toBe("/api/channels/chan-1/members/agent/agent-9");
    expect(init?.method).toBe("PATCH");
    expect(String(init?.body)).toContain('"manager"');
  });
});
