import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { researchV6FixtureDelta } from "./research-v6-fixtures";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubResponse(body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

describe("ApiClient Research V6 Delta boundaries", () => {
  it("keeps an explicit null response as no available Delta", async () => {
    stubResponse(null);
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.getResearchV6ProjectionDeltaPage("run-1", 0),
    ).resolves.toBeNull();
  });

  it("degrades a malformed successful Delta to no available Delta", async () => {
    stubResponse({ from_sequence_exclusive: 0, through_sequence: "1" });
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.getResearchV6ProjectionDeltaPage("run-1", 0),
    ).resolves.toBeNull();
  });

  it("accepts a valid Delta response", async () => {
    const delta = researchV6FixtureDelta();
    stubResponse(delta);
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.getResearchV6ProjectionDeltaPage("run-1", delta.from_sequence_exclusive),
    ).resolves.toMatchObject({ through_sequence: delta.through_sequence });
  });

  it("degrades a malformed resume verdict to a safe resync request", async () => {
    stubResponse({ ok: false });
    const client = new ApiClient("https://api.example.test");
    await expect(client.resumeResearchV6Projection("run-1", 0)).resolves.toEqual({
      ok: false,
      resync_required: true,
    });
  });
});
