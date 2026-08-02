// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ResearchReportCitation, ResearchSource } from "@multica/core/types";
import { InlineCitations } from "./report-inline-citations";
import { rewriteCitationRefs } from "./report-inline-citations-utils";

afterEach(() => cleanup());

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (_fn: unknown, params?: Record<string, unknown>) => `t:${params?.label ?? ""}`,
  }),
}));

vi.mock("./report-citation-card", () => ({
  InlineFootnoteCard: ({ citation, onLocate }: {
    citation: ResearchReportCitation;
    onLocate?: (id: string) => void;
  }) => (
    <div data-testid="mock-inline-footnote" data-citation-id={citation.id}>
      {citation.label}
      <button type="button" data-testid="mock-locate" onClick={() => onLocate?.(citation.id)}>
        locate
      </button>
    </div>
  ),
}));

vi.mock("./report-citation-resolve", () => ({
  EMPTY_REPORT_SOURCE_REFS: [],
  resolveCitationSource: () => null,
}));

const citations: ResearchReportCitation[] = [
  { id: "c1", index: 1, source_id: "s1", label: "[1]" },
  { id: "c2", index: 2, source_id: "s2", label: "[2]" },
];

const live: ResearchSource[] = [
  {
    id: "s1",
    url: "https://a.com",
    title: "A",
    credibility_weight: 4,
    source_class: "web",
  } as unknown as ResearchSource,
];

describe("rewriteCitationRefs", () => {
  it("rewrites [^n] and [n] tokens matching known indexes", () => {
    const md = "See [^1] and [2] for details, plus [99] which is unknown.";
    expect(rewriteCitationRefs(md, citations)).toBe(
      "See [[cit:c1]] and [[cit:c2]] for details, plus [99] which is unknown.",
    );
  });

  it("handles custom labels", () => {
    const md = "Custom [refA] here.";
    const custom: ResearchReportCitation[] = [
      { ...citations[0], label: "[refA]" } as ResearchReportCitation,
    ];
    expect(rewriteCitationRefs(md, custom)).toBe("Custom [refA] here.");
  });

  it("returns original when no citations", () => {
    const md = "No refs [1] here.";
    expect(rewriteCitationRefs(md, [])).toBe(md);
  });
});

describe("InlineCitations", () => {
  it("renders clickable citation refs and opens footnote on click", async () => {
    const onLocate = vi.fn();
    const md = "Body [^1] text.";
    render(
      <InlineCitations
        markdown={md}
        citations={citations}
        liveSources={live}
        onLocateCitation={onLocate}
      >
        {(_rewritten, renderCitation) => (
          <div>{renderCitation({ citationId: "c1", label: "[1]" })}</div>
        )}
      </InlineCitations>,
    );

    const ref = screen.getByTestId("research-inline-citation");
    expect(ref.textContent).toBe("[1]");

    await userEvent.click(ref);
    expect(screen.getByTestId("mock-inline-footnote")).toBeTruthy();

    await userEvent.click(screen.getByTestId("mock-locate"));
    expect(onLocate).toHaveBeenCalledWith("c1");
  });

  it("renders label from citation record", () => {
    const md = "Body [^1] text.";
    render(
      <InlineCitations markdown={md} citations={citations} liveSources={live}>
        {(_rewritten, renderCitation) => (
          <div>{renderCitation({ citationId: "c1", label: "[?]" })}</div>
        )}
      </InlineCitations>,
    );
    const ref = screen.getByTestId("research-inline-citation");
    expect(ref.textContent).toBe("[1]");
  });
});
