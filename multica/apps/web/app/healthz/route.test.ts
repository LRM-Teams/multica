import { describe, expect, it } from "vitest";
import { GET } from "./route";

describe("GET /healthz", () => {
  it("returns an uncached process-local healthy response", async () => {
    const response = GET();

    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toEqual({ status: "ok" });
    expect(response.headers.get("cache-control")).toBe("no-store");
  });
});
