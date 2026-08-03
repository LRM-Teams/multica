// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { NormalizedReportStructured } from "@multica/core/research";
import {
  buildOutlineItems,
  outlineSectionDomId,
  resolveActiveOutlineId,
} from "./report-outline";

const labels = { sources: "Sources", body: "Report" };

describe("buildOutlineItems", () => {
  it("flattens nested children and keeps ≥2 levels (LRM-829)", () => {
    const normalized = {
      kind: "v1",
      render_mode: "structured",
      structured: {
        schema_version: 1,
        title: "T",
        outline: [
          { id: "sec-a", title: "A", level: 1, children: ["sec-a1"] },
          { id: "sec-a1", title: "A.1", level: 2, children: [] },
          { id: "sec-b", title: "B", level: 1, children: [] },
        ],
        sections: [
          { id: "sec-a", title: "A", level: 1, markdown: "", citation_ids: [] },
          { id: "sec-a1", title: "A.1", level: 2, markdown: "", citation_ids: [] },
          { id: "sec-b", title: "B", level: 1, markdown: "", citation_ids: [] },
        ],
        citations: [],
        sources: [],
      },
    } as NormalizedReportStructured;

    const items = buildOutlineItems(normalized, labels);
    expect(items.some((i) => i.level === 1)).toBe(true);
    expect(items.some((i) => i.level === 2)).toBe(true);
    expect(items.map((i) => i.id)).toEqual([
      "sec-a",
      "sec-a1",
      "sec-b",
      "sources",
    ]);
  });

  it("nests flat outline under body so the tree is never single-level", () => {
    const normalized = {
      kind: "v1",
      render_mode: "structured",
      structured: {
        schema_version: 1,
        title: "T",
        outline: [
          { id: "sec-a", title: "A", level: 1, children: [] },
          { id: "sec-b", title: "B", level: 1, children: [] },
        ],
        sections: [
          { id: "sec-a", title: "A", level: 1, markdown: "", citation_ids: [] },
          { id: "sec-b", title: "B", level: 1, markdown: "", citation_ids: [] },
        ],
        citations: [],
        sources: [],
      },
    } as NormalizedReportStructured;

    const items = buildOutlineItems(normalized, labels);
    expect(items[0]).toMatchObject({ id: "body", level: 1 });
    expect(items.filter((i) => i.level === 2).map((i) => i.id)).toEqual([
      "sec-a",
      "sec-b",
    ]);
  });

  it("markdown_only uses body + sources as a two-level shell", () => {
    const normalized = {
      kind: "legacy_empty",
      render_mode: "markdown_only",
      structured: null,
    } as NormalizedReportStructured;
    expect(buildOutlineItems(normalized, labels)).toEqual([
      { id: "body", title: "Report", level: 1 },
      { id: "sources", title: "Sources", level: 2 },
    ]);
  });
});

describe("resolveActiveOutlineId", () => {
  it("picks the last section whose top is above the sticky offset", () => {
    const offsets = [
      { id: "a", offsetTop: 0 },
      { id: "b", offsetTop: 200 },
      { id: "c", offsetTop: 400 },
    ];
    expect(resolveActiveOutlineId(0, offsets, 72)).toBe("a");
    expect(resolveActiveOutlineId(250, offsets, 72)).toBe("b");
    expect(resolveActiveOutlineId(500, offsets, 72)).toBe("c");
  });
});

describe("outlineSectionDomId", () => {
  it("maps body/sources and section ids", () => {
    expect(outlineSectionDomId("body")).toBe("report-body");
    expect(outlineSectionDomId("sources")).toBe("report-sources");
    expect(outlineSectionDomId("sec-a")).toBe("report-sec-sec-a");
  });
});
