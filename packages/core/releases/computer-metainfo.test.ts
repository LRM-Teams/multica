import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchTestComputerRelease } from "./computer-metainfo";

describe("fetchTestComputerRelease", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns the exact preview tag from canonical metainfo", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            schema_version: 1,
            environments: {
              test: { tag: "v0.4.24-alpha.6" },
            },
          }),
          { status: 200 },
        ),
      ),
    );

    await expect(fetchTestComputerRelease()).resolves.toEqual({
      tag: "v0.4.24-alpha.6",
    });
  });

  it("fails closed when Test does not point at a preview release", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            schema_version: 1,
            environments: {
              test: { tag: "v0.4.24" },
            },
          }),
          { status: 200 },
        ),
      ),
    );

    await expect(fetchTestComputerRelease()).rejects.toThrow(
      "invalid Test release",
    );
  });

  it("reports an unavailable release feed", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response(null, { status: 503 })),
    );

    await expect(fetchTestComputerRelease()).rejects.toThrow("503");
  });
});
