import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { researchKeys } from "./queries";
import { evictResearchSessionQueries } from "./session-cache";

describe("evictResearchSessionQueries", () => {
  it("removes every D5 cache family for only the deleted session", async () => {
    const qc = new QueryClient();
    const targetKeys = [
      researchKeys.snapshot("ws1", "deleted"),
      researchKeys.presence("ws1", "deleted"),
      researchKeys.productRounds("ws1", "deleted"),
      researchKeys.graphTyped("ws1", "deleted", { limit: 100, offset: 0 }),
      researchKeys.graphTypedInfinite("ws1", "deleted"),
      ["research-canvas", "ws1", "deleted", "capability", "run-1"],
      ["research-canvas", "ws1", "deleted", "snapshot", "v5"],
    ];
    const retainedKeys = [
      researchKeys.sessions("ws1"),
      researchKeys.snapshot("ws1", "retained"),
      researchKeys.snapshot("ws2", "deleted"),
    ];
    for (const key of [...targetKeys, ...retainedKeys]) {
      qc.setQueryData(key, { present: true });
    }

    await evictResearchSessionQueries(qc, "ws1", "deleted");

    for (const key of targetKeys) expect(qc.getQueryData(key)).toBeUndefined();
    for (const key of retainedKeys) expect(qc.getQueryData(key)).toEqual({ present: true });
  });
});
