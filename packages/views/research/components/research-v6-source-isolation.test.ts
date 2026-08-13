import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "research-session-page.tsx"),
  "utf8",
);

describe("Research D5 canonical projection source isolation", () => {
  it("scopes V5 query state and pagination to an active V5 gateway", () => {
    expect(source).toContain(
      'const canvasUsesV5 = projectionGateway.status === "v5"',
    );
    expect(source).toContain("canvasUsesV5 && typedGraphLoading");
    expect(source).toContain("canvasUsesV5 && typedGraphError");
    expect(source).toContain(
      "typedGraphHasNextPage={canvasUsesV5 && typedGraphHasNextPage === true}",
    );
    expect(source).toContain("canvasUsesV5 && typedGraphHasNextPage");
  });

  it("uses the selected projection source for chrome and node detail resolution", () => {
    expect(source).toContain("typedGraphNodes={displayTypedGraph?.nodes ?? []}");
    expect(source.match(/typedGraph: displayTypedGraph/g)?.length).toBeGreaterThanOrEqual(
      3,
    );
    expect(source).toContain(
      "enrichResearchNodeForDetail(base, displayTypedGraph)",
    );
  });
});
