import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "research-session-page.tsx"),
  "utf8",
);

describe("Research D5 canonical projection source isolation", () => {
  it("uses only V5 or the authoritative Director V6 projection", () => {
    expect(source).toContain(
      'const projectionSource = directorV6Enabled ? "v6" : "v5"',
    );
    expect(source).toContain(
      "directorV6Enabled ? directorCanvas.canvas?.graph : typedGraph",
    );
    expect(source).not.toContain("projectionGateway");
    expect(source).not.toContain("getResearchV6ProjectionSnapshot");
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

  it("does not derive legacy stage presentation for Director runs", () => {
    expect(source).toMatch(
      /const startedStages = directorV6Enabled\s*\? \[\]\s*:\s*RESEARCH_STAGE_ORDER\.filter/,
    );
    expect(source).toMatch(
      /fallbackName=\{[\s\S]*directorV6Enabled[\s\S]*assignedDirectorAgent/,
    );
  });

  it("refetches the selected durable Work activity on matching task progress", () => {
    for (const event of [
      '"task:running"',
      '"task:progress"',
      '"task:completed"',
      '"task:failed"',
      '"task:cancelled"',
    ]) {
      expect(source).toContain(event);
    }
    expect(source).toContain("progress.task_id === inboxTaskId");
    expect(source).toContain("void refetchDirectorWorkActivity()");
  });
});
