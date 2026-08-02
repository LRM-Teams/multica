import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ResearchSource } from "@multica/core/types";
import { ReportSourceTable } from "./report-source-table";
import { weightTier } from "./report-weight";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        reader: {
          col_weight: "Weight",
          col_type: "Type",
          col_source: "Source",
          col_purpose: "Purpose",
          source_failed_badge: "Fetch failed",
          source_failed_purpose: "Excluded from citation numbering",
          source_fail_fetch: "Source fetch failed",
          source_fail_timeout: "Fetch timed out",
          source_fail_http: "HTTP error while fetching",
          source_fail_invalid_url: "Invalid source URL",
          source_fail_missing: "Source missing",
          source_fail_unknown: "Source unavailable",
          citation_untitled: "Untitled source",
        },
      }),
  }),
}));

function source(partial: Partial<ResearchSource> & { id: string }): ResearchSource {
  return {
    session_id: "s1",
    url: "https://example.com",
    title: "Example",
    source_class: "docs",
    credibility_weight: 0.9,
    stance: "neutral",
    relevance: 0.8,
    summary: "Purpose note",
    excerpt: "",
    payload: {},
    created_at: "",
    updated_at: "",
    ...partial,
  };
}

describe("weightTier", () => {
  it("bins high / mid / low", () => {
    expect(weightTier(0.9)).toBe("hi");
    expect(weightTier(0.7)).toBe("mid");
    expect(weightTier(0.4)).toBe("lo");
  });
});

describe("ReportSourceTable", () => {
  it("renders a real table with weight, type, linked source, and purpose", () => {
    render(
      <ReportSourceTable
        sources={[
          source({ id: "a", title: "Colyseus Docs", credibility_weight: 0.9 }),
          source({
            id: "b",
            title: "Forum",
            source_class: "forum",
            credibility_weight: 0.55,
            summary: "Community notes",
          }),
        ]}
      />,
    );
    expect(screen.getByRole("columnheader", { name: "Weight" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Colyseus Docs" })).toHaveAttribute(
      "href",
      "https://example.com",
    );
    expect(screen.getByText("0.90")).toBeInTheDocument();
    expect(screen.getByText("forum")).toBeInTheDocument();
    expect(screen.getByText("Community notes")).toBeInTheDocument();
    // No raw markdown pipe table
    expect(screen.queryByText(/\|/)).not.toBeInTheDocument();
  });

  it("LRM-834: annotates failed rows with a readable reason (no raw ETIMEDOUT)", () => {
    render(
      <ReportSourceTable
        sources={[
          source({ id: "ok", title: "Good Docs" }),
          source({
            id: "bad",
            title: "Broken",
            payload: {
              fetch_failed: true,
              status: "timeout",
              failure_message: "ETIMEDOUT",
            },
          }),
        ]}
      />,
    );
    const rows = screen.getAllByTestId("research-source-row");
    expect(rows[1]).toHaveAttribute("data-source-failed", "true");
    const reason = screen.getByTestId("research-source-fail-reason").textContent ?? "";
    expect(reason).toContain("Fetch timed out");
    expect(reason).not.toMatch(/ETIMEDOUT/i);
    expect(screen.getByText("Excluded from citation numbering")).toBeInTheDocument();
  });

});
