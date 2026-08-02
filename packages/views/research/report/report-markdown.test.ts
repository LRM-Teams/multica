import { describe, expect, it } from "vitest";
import type { ResearchReport } from "@multica/core/types";
import { buildReportMarkdown } from "./report-markdown";

const baseReport: ResearchReport = {
  id: "r1",
  session_id: "s1",
  revision: 2,
  content_md: "## Findings\n\nMilvus wins.\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n",
  structured: {},
  created_at: "",
  updated_at: "",
};

describe("buildReportMarkdown (LRM-831)", () => {
  it("returns trimmed content_md verbatim when present", () => {
    const md = buildReportMarkdown({ ...baseReport, content_md: "  ## Hello  " });
    expect(md).toBe("## Hello");
  });

  it("reconstructs heading hierarchy and footnotes from structured payload", () => {
    const report: ResearchReport = {
      ...baseReport,
      content_md: "",
      structured: {
        schema_version: 1,
        title: "Vector DB comparison",
        outline: [],
        sections: [
          {
            id: "s1",
            title: "Findings",
            level: 1,
            markdown: "Milvus recall higher.[^1]",
            citation_ids: ["c1"],
          },
          {
            id: "s2",
            title: "Detail",
            level: 2,
            markdown: "| k | v |\n| --- | --- |\n| a | 1 |",
            citation_ids: ["c2"],
          },
        ],
        citations: [
          { id: "c1", index: 1, source_id: "src1", label: "[1]", quote: "0.94" },
          { id: "c2", index: 2, source_id: "src2", label: "[2]" },
        ],
        sources: [
          {
            source_id: "src1",
            title: "Milvus Docs",
            url: "https://milvus.io",
            credibility_weight: 0.92,
            source_class: "docs",
          },
          {
            source_id: "src2",
            title: "Qdrant Blog",
            url: "https://qdrant.tech",
            credibility_weight: 0.8,
            source_class: "blog",
          },
        ],
        conclusion: "Milvus leads.",
      },
    };
    const md = buildReportMarkdown(report);
    expect(md).toContain("# Vector DB comparison");
    expect(md).toContain("## Findings");
    expect(md).toContain("### Detail");
    expect(md).toContain("Milvus recall higher.[^1]");
    expect(md).toContain("| k | v |");
    expect(md).toContain("[1] [Milvus Docs](https://milvus.io)");
    expect(md).toContain("> 0.94");
    expect(md).toContain("[2] [Qdrant Blog](https://qdrant.tech)");
  });

  it("keeps citation numbering labels when no URL is known", () => {
    const report: ResearchReport = {
      ...baseReport,
      content_md: "",
      structured: {
        schema_version: 1,
        title: "T",
        outline: [],
        sections: [
          { id: "s1", title: "S", level: 1, markdown: "x", citation_ids: ["c1"] },
        ],
        citations: [{ id: "c1", index: 3, source_id: "srcX", label: "[3]" }],
        sources: [
          {
            source_id: "srcX",
            title: "Unknown",
            url: "",
            credibility_weight: 0.5,
            source_class: "other",
          },
        ],
      },
    };
    const md = buildReportMarkdown(report);
    expect(md).toContain("[3] Unknown");
  });

  it("returns empty string for missing/legacy payload", () => {
    expect(buildReportMarkdown(null)).toBe("");
    expect(buildReportMarkdown({ ...baseReport, content_md: "", structured: {} })).toBe("");
  });
});
