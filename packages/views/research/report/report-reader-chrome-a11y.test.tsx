// @vitest-environment jsdom

/**
 * LRM-1253 — report-reader header chrome lucide dual-announce:
 * outline/close/Copy/Export icons inside named Buttons must be aria-hidden.
 * File mutex vs 1244 completion-card · 1245 editor · 1247 gallery CSS · 1252 token · 1265 chrome handoff.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen, within } from "@testing-library/react";
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

const report: ResearchReport = {
  id: "r1",
  session_id: "s1",
  revision: 2,
  content_md: "## Findings\n\nMilvus wins.\n",
  structured: {},
  created_at: "",
  updated_at: "",
};

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

const SRC = "report-reader.tsx";

describe("report-reader chrome lucide a11y (LRM-1253)", () => {
  it("source: outline/close/Copy/Export lucide icons declare aria-hidden", () => {
    const src = readSrc(SRC);
    // Decorative lucide inside named Buttons must not dual-announce.
    expect(src).toMatch(/<List\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<X\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Copy\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Download\b[\s\S]{0,60}aria-hidden/);
  });

  it("render: named chrome buttons keep accessible names; icons are aria-hidden", () => {
    render(
      <ReportReader open onClose={vi.fn()} report={report} sources={sources} />,
    );
    const card = screen.getByTestId("research-delivery-modal-card");

    const outline = within(card).getByRole("button", { name: "Outline" });
    expect(outline.querySelector("svg")).toHaveAttribute("aria-hidden", "true");

    const copy = within(card).getByRole("button", { name: "Copy MD" });
    expect(copy.querySelector("svg")).toHaveAttribute("aria-hidden", "true");

    const exp = within(card).getByRole("button", { name: "Export" });
    expect(exp.querySelector("svg")).toHaveAttribute("aria-hidden", "true");

    const close = within(card).getByRole("button", { name: "Close" });
    expect(close.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  });
});
