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
        panel: { delivery: "Sources & report", hide_chat: "Close", report: "Report" },
        reader: {
          outline: "Outline",
          copy_md: "Copy MD",
          copied: "Copied",
          export: "Export",
          meta: `Delivery · v${vars?.revision ?? 1} · ${vars?.count ?? 0} sources`,
          sources_heading: "Weighted source map",
          sources_hint: "hint",
          col_weight: "Weight",
          col_type: "Type",
          col_source: "Source",
          col_purpose: "Purpose",
        },
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
});
