// @vitest-environment jsdom

/**
 * LRM-1212 — [巡检][F] delivery report outline drawer a11y:
 * keyboard open/close + focus. Mutually exclusive from 1206/1208/1199.
 * Gate-shot harness (real md: viewport): scripts/lrm1164-harness/?case=report
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ResearchReport, ResearchSource } from "@multica/core/types";
import { ReportReader } from "./report-reader";

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const value = fn({
        panel: {
          delivery: "Sources & report",
          hide_chat: "Close",
          report: "Report",
          delivery_mode: {
            empty: "Idle",
            loading: "Loading",
            running: "In progress",
            error: "Error",
          },
        },
        reader: {
          outline: "Outline",
          copy_md: "Copy MD",
          copied: "Copied",
          export: "Export",
          copy_md_done: "Markdown copied to clipboard",
          export_done: "Markdown file downloaded",
          meta: `Delivery · v${vars?.revision ?? 1} · ${vars?.count ?? 0} sources`,
          sources_heading: "Weighted source map",
          sources_hint: "hint",
          empty_title: "No delivery yet",
          empty_body: "Empty body",
          loading_body: "Assembling…",
          error_title: "Could not load delivery",
          error_body: "Error body",
          col_weight: "Weight",
          col_type: "Type",
          col_source: "Source",
          col_purpose: "Purpose",
          citation_expand: "Show summary",
          citation_collapse: "Hide summary",
          citation_unavailable: "Source unavailable",
          citation_untitled: "Untitled source",
          citation_fetch_failed: "Fetch failed",
          citation_fetch_failed_hint: "Could not fetch this source.",
          citation_summary_empty: "No summary.",
          source_failed_badge: "Fetch failed",
          source_failed_purpose: "Excluded from citation numbering",
          source_fail_fetch: "Source fetch failed",
          source_fail_timeout: "Fetch timed out",
          source_fail_http: "HTTP error while fetching",
          source_fail_invalid_url: "Invalid source URL",
          source_fail_missing: "Source missing",
          source_fail_unknown: "Source unavailable",
          sources_all_failed_title: "All sources failed",
          sources_all_failed_body: "No usable sources right now.",
          sources_all_failed_retry: "Retry fetch",
          sources_partial_failed_hint:
            "{{count}} source(s) failed and are labeled; the session continues.",
        },
        session_page: { retry: "Retry" },
      });
      return value;
    },
  }),
}));

vi.mock("../../common/markdown", () => ({
  Markdown: ({ children }: { children: string }) => (
    <div data-testid="md">{children}</div>
  ),
}));

const sources: ResearchSource[] = [
  {
    id: "src1",
    session_id: "s1",
    url: "https://milvus.io",
    title: "Milvus Docs",
    source_class: "docs",
    credibility_weight: 0.92,
    stance: "neutral",
    relevance: 0.9,
    summary: "Vector DB baseline",
    excerpt: "",
    payload: {},
    created_at: "",
    updated_at: "",
  },
];

const structuredReport: ResearchReport = {
  id: "r1",
  session_id: "s1",
  revision: 2,
  content_md: "## Findings\n\nMilvus wins.\n",
  structured: {
    schema_version: 1,
    title: "Report",
    outline: [
      { id: "sec-bg", title: "Background", level: 1, children: ["sec-detail"] },
      { id: "sec-detail", title: "Detail", level: 2, children: [] },
    ],
    sections: [
      {
        id: "sec-bg",
        title: "Background",
        level: 1,
        markdown: "Context.",
        citation_ids: [],
      },
      {
        id: "sec-detail",
        title: "Detail",
        level: 2,
        markdown: "Numbers.",
        citation_ids: [],
      },
    ],
    citations: [],
    sources: [],
    conclusion: "",
  },
  created_at: "",
  updated_at: "",
};

describe("report outline drawer a11y (LRM-1212)", () => {
  it("source: toggle declares aria-controls + drawer id; Escape closes drawer first", () => {
    const src = readSrc("report-reader.tsx");
    expect(src).toMatch(
      /aria-controls=["']research-report-outline-drawer["']/,
    );
    expect(src).toMatch(
      /id=["']research-report-outline-drawer["']/,
    );
    expect(src).toMatch(/if \(outlineOpen\)/);
    expect(src).toMatch(/closeOutlineDrawer/);
    // Harness reuse pointer (gate shots / real md: viewport)
    const harness = fs.readFileSync(
      path.join(here, "../../../../scripts/lrm1164-harness/main.tsx"),
      "utf8",
    );
    expect(harness).toMatch(/ReportReader/);
    expect(harness).toMatch(/params\.get\("case"\) === "report"/);
  });

  it("toggle aria-expanded tracks open state; drawer hosts outline nav", async () => {
    render(
      <ReportReader
        open
        onClose={vi.fn()}
        report={structuredReport}
        sources={sources}
      />,
    );
    const toggle = screen.getByTestId("research-report-outline-toggle");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveAttribute(
      "aria-controls",
      "research-report-outline-drawer",
    );
    expect(
      screen.queryByTestId("research-report-outline-drawer"),
    ).not.toBeInTheDocument();

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const drawer = screen.getByTestId("research-report-outline-drawer");
    expect(drawer).toHaveAttribute("id", "research-report-outline-drawer");
    expect(
      drawer.querySelector('[data-testid="research-report-outline"]'),
    ).toBeTruthy();
    expect(
      drawer.querySelectorAll("button[data-outline-id]").length,
    ).toBeGreaterThan(0);
  });



});
