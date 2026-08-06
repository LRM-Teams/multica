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
          sources_partial_failed_hint: "{{count}} source(s) failed and are labeled; the session continues.",
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

  it("LRM-1234: the modal exposes no dismiss scrim node at all", () => {
    const onClose = vi.fn();
    render(<ReportReader open onClose={onClose} report={report} sources={sources} />);
    // First attempt (#2075) kept the full-screen scrim and only marked it
    // aria-hidden + tabIndex=-1. Chromium proved that is not enough: a
    // `tabindex="-1"` node is still FOCUSABLE, so the native dialog focusing
    // steps parked initial focus on it (375x860 / 1280x860 invisible target)
    // and the first Enter/Space still dismissed the report. The node has to go.
    expect(screen.queryByTestId("research-delivery-modal-dismiss-scrim")).toBeNull();
    const dialog = screen.getByRole("dialog");
    expect(dialog.querySelector("button.absolute.inset-0")).toBeNull();
    // Only the header X may expose the Close name.
    expect(screen.getAllByRole("button", { name: "Close" })).toHaveLength(1);
    // Pointer dismiss survives on the dialog's own box (gutter around card).
    fireEvent.click(dialog);
    expect(onClose).toHaveBeenCalledTimes(1);
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
            source_id: "src2",
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
    const src2: ResearchSource = {
      ...sources[0]!,
      id: "src2",
      title: "Qdrant Blog",
      url: "https://qdrant.tech",
      credibility_weight: 0.8,
      source_class: "blog",
    };
    render(
      <ReportReader
        open
        onClose={vi.fn()}
        report={structuredReport}
        sources={[sources[0]!, src2]}
      />,
    );
    const cards = screen.getAllByTestId("research-citation-card");
    expect(cards).toHaveLength(2);
    const citationLink = cards[0]!.querySelector("a");
    expect(citationLink).toHaveAttribute("href", "https://milvus.io");
    expect(citationLink).toHaveAttribute("target", "_blank");
  });

  it("LRM-834: failed sources stay labeled but drop out of citation sequence; all-failed shows retry", () => {
    const onRetry = vi.fn();
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
            markdown: "Milvus recall higher.[^1] Also [^2].",
            citation_ids: ["c1", "c2"],
          },
        ],
        citations: [
          { id: "c1", index: 1, source_id: "src1", label: "[1]", quote: "0.94" },
          { id: "c2", index: 2, source_id: "src-fail", label: "[2]" },
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
      title: "Broken",
      url: "https://example.invalid/x",
      payload: { fetch_failed: true, status: "timeout" },
    };
    const { rerender } = render(
      <ReportReader
        open
        onClose={vi.fn()}
        report={structuredReport}
        sources={[sources[0]!, failSource]}
        onRetry={onRetry}
      />,
    );
    // Partial: session continues; failed row labeled; failed citation excluded.
    expect(screen.getByTestId("research-sources-partial-failed")).toBeInTheDocument();
    expect(screen.getByTestId("research-source-fail-reason").textContent).toContain(
      "Fetch timed out",
    );
    const cards = screen.getAllByTestId("research-citation-card");
    expect(cards).toHaveLength(1);
    expect(cards[0]).toHaveAttribute("data-citation-id", "c1");

    // All failed: clear prompt + retry.
    rerender(
      <ReportReader
        open
        onClose={vi.fn()}
        report={structuredReport}
        sources={[failSource]}
        onRetry={onRetry}
      />,
    );
    expect(screen.getByTestId("research-sources-all-failed")).toBeInTheDocument();
    expect(screen.queryByTestId("research-citation-card")).toBeNull();
    const findings = screen.getByTestId("md").textContent ?? "";
    expect(findings).not.toMatch(/\[\^?1\]/);
    expect(findings).not.toMatch(/\[\^?2\]/);
    fireEvent.click(screen.getByTestId("research-sources-all-failed-retry"));
    expect(onRetry).toHaveBeenCalled();
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


  it("LRM-831: export button downloads markdown and fires toast", async () => {
    const { toast } = await import("sonner");
    const successSpy = vi.spyOn(toast, "success");
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    // jsdom lacks createObjectURL
    URL.createObjectURL = vi.fn(() => "blob:mock");
    URL.revokeObjectURL = vi.fn();

    render(
      <ReportReader open onClose={vi.fn()} report={report} sources={sources} />,
    );
    fireEvent.click(screen.getByRole("button", { name: /^export$/i }));
    expect(clickSpy).toHaveBeenCalled();
    expect(successSpy).toHaveBeenCalledWith("Markdown file downloaded");
    clickSpy.mockRestore();
  });

  it("LRM-1234: no full-screen invisible button; empty-space click still closes", () => {
    const onClose = vi.fn();
    render(<ReportReader open onClose={onClose} report={report} sources={sources} />);
    const dialog = screen.getByTestId("research-delivery-modal");

    // The old overlay was `<button class="absolute inset-0 …">`: focusable, so
    // the dialog focusing steps parked initial focus on an invisible rect.
    expect(dialog.querySelector("button.absolute.inset-0")).toBeNull();

    // Pointer close survives via the dialog's own box (the gutter around card).
    fireEvent.click(dialog);
    expect(onClose).toHaveBeenCalledTimes(1);

    // Clicks inside the reading card must not close.
    fireEvent.click(screen.getByTestId("research-delivery-modal-card"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("LRM-1234: only one exposed Close control inside the report modal", () => {
    render(<ReportReader open onClose={vi.fn()} report={report} sources={sources} />);
    // The invisible full-screen overlay used to duplicate the header X's name.
    const closers = screen.getAllByRole("button", { name: "Close" });
    expect(closers).toHaveLength(1);
    expect(closers[0]?.className ?? "").not.toMatch(/inset-0/);
  });

  it("LRM-1234: every focusable control in the modal has a real layout box", () => {
    render(<ReportReader open onClose={vi.fn()} report={report} sources={sources} />);
    const dialog = screen.getByTestId("research-delivery-modal");
    const focusable = Array.from(
      dialog.querySelectorAll<HTMLElement>(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      ),
    );
    expect(focusable.length).toBeGreaterThan(0);
    // No focus stop may be a full-bleed invisible layer.
    expect(focusable.some((el) => /absolute/.test(el.className) && /inset-0/.test(el.className))).toBe(
      false,
    );
    // Narrow tier keeps the outline toggle first (it is `md:hidden`, so on
    // desktop the first stop is the copy button — both are visible chrome).
    expect(focusable[0]?.getAttribute("data-testid")).toBe(
      "research-report-outline-toggle",
    );
  });

  it("LRM-1234: Escape contract unchanged — drawer first, then the modal", () => {
    const onClose = vi.fn();
    render(<ReportReader open onClose={onClose} report={report} sources={sources} />);
    const dialog = screen.getByRole("dialog");

    fireEvent.click(screen.getByTestId("research-report-outline-toggle"));
    expect(screen.getByTestId("research-report-outline-drawer")).toBeInTheDocument();

    fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
    expect(screen.queryByTestId("research-report-outline-drawer")).toBeNull();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
