import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

describe("Research node report editorial shell", () => {
  it("keeps overview, lineage, and evidence sections navigable without a duplicate close", () => {
    const source = fs.readFileSync(
      path.join(__dirname, "research-node-report-modal.tsx"),
      "utf8",
    );

    expect(source).toContain('href="#node-report-overview"');
    expect(source).toContain('href="#node-report-lineage"');
    expect(source).toContain('href="#node-report-detail"');
    expect(source).toContain('id="node-report-overview"');
    expect(source).toContain('id="node-report-lineage"');
    expect(source).toContain('id="node-report-detail"');
    expect(source).toContain("showClose={false}");
  });
});
