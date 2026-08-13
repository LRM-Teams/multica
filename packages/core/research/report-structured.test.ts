import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  normalizeReportStructured,
  isResearchReportStructuredV1,
  RESEARCH_REPORT_SCHEMA_VERSION,
} from "./report-structured";

const fixturesDir = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../../docs/research/fixtures",
);

function loadFixture(name: string): unknown {
  return JSON.parse(readFileSync(join(fixturesDir, name), "utf8"));
}

describe("normalizeReportStructured", () => {
  it("treats missing / empty structured as markdown_only legacy", () => {
    expect(normalizeReportStructured(undefined).render_mode).toBe("markdown_only");
    expect(normalizeReportStructured(null).render_mode).toBe("markdown_only");
    expect(normalizeReportStructured({}).kind).toBe("legacy_empty");
    expect(normalizeReportStructured({}).render_mode).toBe("markdown_only");
  });

  it("treats objects without schema_version as legacy markdown_only", () => {
    const result = normalizeReportStructured({ title: "ad-hoc" });
    expect(result.kind).toBe("legacy_empty");
    expect(result.structured).toBeNull();
    expect(result.render_mode).toBe("markdown_only");
  });

  it("parses schema_version 1 from the mock fixture", () => {
    const example = loadFixture("report-v1.example.json") as {
      structured: unknown;
    };
    const result = normalizeReportStructured(example.structured);
    expect(result.kind).toBe("v1");
    expect(result.render_mode).toBe("structured");
    expect(result.structured?.schema_version).toBe(RESEARCH_REPORT_SCHEMA_VERSION);
    expect(result.structured?.outline).toHaveLength(2);
    expect(result.structured?.sections).toHaveLength(2);
    expect(result.structured?.citations).toHaveLength(1);
    expect(result.structured?.sources).toHaveLength(1);
    expect(result.structured?.citations[0]?.source_id).toBe(
      result.structured?.sources[0]?.source_id,
    );
  });

  it("does not manufacture a source weight for structured reports", () => {
    const result = normalizeReportStructured({
      schema_version: 1,
      title: "Report",
      outline: [],
      sections: [],
      citations: [],
      sources: [{ source_id: "src1", title: "Unknown score" }],
    });
    expect(result.structured?.sources[0]).not.toHaveProperty("credibility_weight");
  });

  it("legacy empty fixture stays markdown_only", () => {
    const example = loadFixture("report-legacy-empty.example.json") as {
      structured: unknown;
    };
    const result = normalizeReportStructured(example.structured);
    expect(result.kind).toBe("legacy_empty");
    expect(result.render_mode).toBe("markdown_only");
  });

  it("degrades unknown schema_version to readonly_markdown", () => {
    const result = normalizeReportStructured({
      schema_version: 99,
      title: "future",
      outline: [],
      sections: [],
      citations: [],
      sources: [],
    });
    expect(result.kind).toBe("unknown");
    expect(result.render_mode).toBe("readonly_markdown");
    expect(result.structured).toBeNull();
  });

  it("type-guards v1 payloads", () => {
    const example = loadFixture("report-v1.example.json") as { structured: unknown };
    expect(isResearchReportStructuredV1(example.structured)).toBe(true);
    expect(isResearchReportStructuredV1({})).toBe(false);
  });
});
