// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchReportCitation, ResearchSource } from "@multica/core/types";
import {
  filterCitationsExcludingFailed,
  partitionSourcesByFailure,
  resolveSourceFailureReasonCode,
  resolveSourcesFailureMode,
  stripCitationRefs,
} from "./report-source-degrade";

function source(partial: Partial<ResearchSource> & { id: string }): ResearchSource {
  return {
    session_id: "s1",
    url: "https://example.com",
    title: "Example",
    source_class: "docs",
    credibility_weight: 0.9,
    stance: "neutral",
    relevance: 0.8,
    summary: "",
    excerpt: "",
    payload: {},
    created_at: "",
    updated_at: "",
    ...partial,
  };
}

describe("report-source-degrade (LRM-834)", () => {
  it("partitions ok vs failed sources", () => {
    const ok = source({ id: "ok" });
    const failed = source({
      id: "bad",
      payload: { fetch_failed: true },
      title: "",
    });
    const parts = partitionSourcesByFailure([ok, failed]);
    expect(parts.ok.map((s) => s.id)).toEqual(["ok"]);
    expect(parts.failed.map((s) => s.id)).toEqual(["bad"]);
  });

  it("resolves failure mode none / partial / all", () => {
    const ok = source({ id: "ok" });
    const failed = source({ id: "bad", payload: { fetch_failed: true } });
    expect(resolveSourcesFailureMode([])).toBe("none");
    expect(resolveSourcesFailureMode([ok])).toBe("none");
    expect(resolveSourcesFailureMode([ok, failed])).toBe("partial");
    expect(resolveSourcesFailureMode([failed])).toBe("all");
  });

  it("maps timeout / http reason codes from payload", () => {
    expect(
      resolveSourceFailureReasonCode(
        source({ id: "t", payload: { fetch_failed: true, status: "timeout" } }),
      ),
    ).toBe("timeout");
    expect(
      resolveSourceFailureReasonCode(
        source({
          id: "et",
          payload: { fetch_failed: true, failure_message: "ETIMEDOUT" },
        }),
      ),
    ).toBe("timeout");
    expect(
      resolveSourceFailureReasonCode(
        source({ id: "h", payload: { fetch_failed: true, failure_reason: "http_403" } }),
      ),
    ).toBe("http");
  });

  it("strips excluded citation tokens from Findings prose", () => {
    const md = "Milvus recall higher.[^1] Also [^2].";
    const excluded: ResearchReportCitation[] = [
      { id: "c1", index: 1, source_id: "a", label: "[1]" },
      { id: "c2", index: 2, source_id: "b", label: "[2]" },
    ];
    expect(stripCitationRefs(md, excluded)).toBe("Milvus recall higher. Also.");
    expect(
      stripCitationRefs(md, [{ id: "c2", index: 2, source_id: "b", label: "[2]" }]),
    ).toBe("Milvus recall higher.[^1] Also.");
  });

  it("excludes failed sources from the citation sequence", () => {
    const citations: ResearchReportCitation[] = [
      { id: "c1", index: 1, source_id: "ok", label: "[1]" },
      { id: "c2", index: 2, source_id: "bad", label: "[2]" },
      { id: "c3", index: 3, source_id: "missing", label: "[3]" },
    ];
    const live = [
      source({ id: "ok" }),
      source({ id: "bad", payload: { fetch_failed: true } }),
    ];
    const kept = filterCitationsExcludingFailed(citations, live, []);
    expect(kept.map((c) => c.id)).toEqual(["c1"]);
  });

  it("hides the whole citation sequence when every live source failed", () => {
    const citations: ResearchReportCitation[] = [
      { id: "c1", index: 1, source_id: "ok", label: "[1]" },
      { id: "c2", index: 2, source_id: "bad", label: "[2]" },
    ];
    const live = [source({ id: "bad", payload: { fetch_failed: true } })];
    const kept = filterCitationsExcludingFailed(citations, live, [
      {
        source_id: "ok",
        title: "Milvus Docs",
        url: "https://milvus.io",
        credibility_weight: 0.9,
        source_class: "docs",
      },
    ]);
    expect(kept).toEqual([]);
  });
});
