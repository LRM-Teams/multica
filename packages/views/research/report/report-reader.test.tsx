import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchReport, ResearchSource } from "@multica/core/types";
import { ReportReader } from "./report-reader";

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
        },
        session_page: { retry: "Retry" },
      });
      return value;
    },
  }),
}));

vi.mock("../../common/markdown", () => ({
  Markdown: ({ children }: { children: string }) => <div data-testid="md">{children}</div>,
}));

const report: ResearchReport = {
  id: "r1",
  session_id: "s1",
  revision: 2,
  content_md: "## Findings\n\nMilvus wins.\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n",
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

describe("ReportReader", () => {
  it("opens as a centered body portal modal, not a canvas-corner float", () => {
    render(
      <ReportReader open onClose={vi.fn()} report={report} sources={sources} />,
    );
    const modal = screen.getByTestId("research-delivery-modal");
    const card = screen.getByTestId("research-delivery-modal-card");
    expect(modal).toBeInTheDocument();
    expect(modal.tagName).toBe("DIALOG");
    expect(modal.parentElement).toBe(document.body);
    // Anti-example used absolute bottom-4 + md:w-[420px] corner chip — ban that.
    expect(modal.className).not.toMatch(/absolute/);
    expect(card.className).not.toMatch(/md:w-\[420px\]/);
    expect(document.querySelector("pre.whitespace-pre-wrap")).toBeNull();
  });

  it("opens as an HTML reading shell, not a raw pre/whitespace dump", () => {
    render(
      <ReportReader open onClose={vi.fn()} report={report} sources={sources} />,
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByTestId("md")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Milvus Docs" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Weight" })).toBeInTheDocument();
    // Default reading state must not show a monospaced raw pre of the whole report
    expect(document.querySelector("pre.whitespace-pre-wrap")).toBeNull();
  });

  it("closes on Escape (dialog cancel)", () => {
    const onClose = vi.fn();
    render(<ReportReader open onClose={onClose} report={report} sources={sources} />);
    const dialog = screen.getByRole("dialog");
    fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
    expect(onClose).toHaveBeenCalled();
  });

  it("renders a centered modal shell (not a corner float)", () => {
    render(<ReportReader open onClose={vi.fn()} report={report} sources={sources} />);
    const dialog = screen.getByTestId("research-delivery-modal");
    expect(dialog.className).toContain("justify-center");
    expect(dialog.className).toContain("sm:items-center");
    const card = dialog.querySelector("[role='document']");
    expect(card?.className).toMatch(/sm:max-w-/);
    expect(card?.className).toMatch(/sm:rounded/);
  });

  it("exposes four-state delivery modes inside the centered modal (LRM-993)", () => {
    const { rerender } = render(
      <ReportReader open onClose={vi.fn()} report={null} sources={[]} sessionStatus="drafting" />,
    );
    expect(screen.getByTestId("research-delivery-modal-card")).toHaveAttribute(
      "data-delivery-mode",
      "empty",
    );
    expect(screen.getByTestId("research-delivery-empty")).toBeInTheDocument();

    rerender(
      <ReportReader
        open
        onClose={vi.fn()}
        report={null}
        sources={[]}
        sessionStatus="running"
      />,
    );
    expect(screen.getByTestId("research-delivery-modal-card")).toHaveAttribute(
      "data-delivery-mode",
      "loading",
    );
    expect(screen.getByTestId("research-delivery-loading")).toBeInTheDocument();

    rerender(
      <ReportReader
        open
        onClose={vi.fn()}
        report={null}
        sources={[]}
        error="boom"
        onRetry={() => {}}
      />,
    );
    expect(screen.getByTestId("research-delivery-modal-card")).toHaveAttribute(
      "data-delivery-mode",
      "error",
    );
    expect(screen.getByTestId("research-delivery-error")).toBeInTheDocument();

    rerender(
      <ReportReader open onClose={vi.fn()} report={report} sources={sources} />,
    );
    expect(screen.getByTestId("research-delivery-modal-card")).toHaveAttribute(
      "data-delivery-mode",
      "running",
    );
    expect(screen.getByTestId("research-delivery-mode")).toHaveAttribute(
      "data-delivery-mode",
      "running",
    );
  });

  it("renders in-body citation cards for structured report sections (LRM-821)", () => {
    const structuredReport: ResearchReport = {
      ...report,
      structured: {
        schema_version: 1,
        title: "Vector DB comparison",
        outline: [{ id: "sec-find", title: "Findings", level: 1, children: [] }],
        sections: [
          {
            id: "sec-find",
            title: "Findings",
            level: 1,
            markdown: "Milvus recall higher.[^1]",
            citation_ids: ["c1", "c2"],
          },
        ],
        citations: [
          {
            id: "c1",
            index: 1,
            source_id: "src1",
            label: "[1]",
            quote: "0.94",
          },
          {
            id: "c2",
            index: 2,
            source_id: "src-fail",
            label: "[2]",
          },
        ],
        sources: [
          {
            source_id: "src1",
            title: "Milvus Docs",
            url: "https://milvus.io",
            credibility_weight: 0.92,
            source_class: "docs",
          },
        ],
        conclusion: "Milvus leads.",
      },
    };
    const failSource: ResearchSource = {
      ...sources[0]!,
      id: "src-fail",
      title: "",
      url: "https://example.invalid/x",
      payload: { fetch_failed: true },
    };
    render(
      <ReportReader
        open
        onClose={vi.fn()}
        report={structuredReport}
        sources={[sources[0]!, failSource]}
      />,
    );
    const cards = screen.getAllByTestId("research-citation-card");
    expect(cards).toHaveLength(2);
    const citationLink = cards[0]!.querySelector("a");
    expect(citationLink).toHaveAttribute("href", "https://milvus.io");
    expect(citationLink).toHaveAttribute("target", "_blank");
    expect(screen.getByText("Source unavailable")).toBeInTheDocument();
    expect(cards[1]).toHaveAttribute("data-degraded", "true");
  });

  it("LRM-829: outline tree is ≥2 levels and click scrolls + flashes the section", () => {
    const scrollSpy = vi.fn();
    window.HTMLElement.prototype.scrollIntoView = scrollSpy;
    const structuredReport: ResearchReport = {
      ...report,
      structured: {
        schema_version: 1,
        title: "Vector DB comparison",
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
    };
    render(
      <ReportReader open onClose={vi.fn()} report={structuredReport} sources={sources} />,
    );
    const outline = screen.getByTestId("research-report-outline");
    const levels = [...outline.querySelectorAll("[data-outline-level]")].map((el) =>
      el.getAttribute("data-outline-level"),
    );
    expect(levels).toContain("1");
    expect(levels).toContain("2");
    fireEvent.click(screen.getByRole("button", { name: "Detail" }));
    expect(scrollSpy).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
    expect(document.getElementById("report-sec-sec-detail")?.classList.contains(
      "research-anchor-flash",
    )).toBe(true);
  });
});
