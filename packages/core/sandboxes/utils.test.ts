import { describe, expect, it } from "vitest";
import { effectiveSandboxNodeStatus, SANDBOX_NODE_STALE_MS } from "./utils";

describe("effectiveSandboxNodeStatus", () => {
  const now = Date.parse("2026-07-03T12:00:00.000Z");

  it("returns stored status when not online", () => {
    expect(effectiveSandboxNodeStatus("offline", "2026-07-03T11:59:50.000Z", now)).toBe("offline");
  });

  it("returns online for a fresh last_seen_at", () => {
    expect(
      effectiveSandboxNodeStatus("online", "2026-07-03T11:59:50.000Z", now),
    ).toBe("online");
  });

  it("returns offline when last_seen_at exceeds the stale window", () => {
    const staleAt = new Date(now - SANDBOX_NODE_STALE_MS - 1).toISOString();
    expect(effectiveSandboxNodeStatus("online", staleAt, now)).toBe("offline");
  });

  it("returns offline when last_seen_at is missing", () => {
    expect(effectiveSandboxNodeStatus("online", null, now)).toBe("offline");
  });
});
