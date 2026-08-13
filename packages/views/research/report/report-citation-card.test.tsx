import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchReportCitation, ResearchSource } from "@multica/core/types";
import { ReportCitationCard, ReportCitationList } from "./report-citation-card";
import { isCitationSourceDegraded } from "./report-citation-resolve";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) =>
      fn({
        reader: {
          citation_expand: "Show summary",
          citation_collapse: "Hide summary",
          citation_unavailable: "Source unavailable",
          citation_untitled: "Untitled source",
          citation_fetch_failed: "Fetch failed",
          citation_fetch_failed_hint: "Could not fetch this source.",
          citation_summary_empty: "No summary.",
          citation_anchor: `Locate citation ${vars?.label ?? ""}`.trim(),
          citations_label: "Citations",
        },
      }),
  }),
}));

const citation: ResearchReportCitation = {
  id: "c1",
  index: 1,
  source_id: "src1",
  label: "[1]",
  quote: "recall@10 0.94",
};

const live: ResearchSource = {
  id: "src1",
  session_id: "s1",
  url: "https://milvus.io/docs/benchmarks",
  title: "Milvus benchmarks",
  source_class: "primary",
  credibility_weight: 0.9,
  stance: "supports",
  relevance: 0.85,
  summary: "Official Milvus ANN benchmark numbers",
  excerpt: "recall@10 0.94 at 10k QPS",
  payload: {},
  created_at: "",
  updated_at: "",
};

describe("isCitationSourceDegraded", () => {
  it("flags missing and fetch_failed sources", () => {
    expect(isCitationSourceDegraded(null)).toBe(true);
    expect(
      isCitationSourceDegraded({
        ...live,
        title: "",
        payload: { fetch_failed: true },
      }),
    ).toBe(true);
    expect(isCitationSourceDegraded(live)).toBe(false);
  });
});

describe("ReportCitationCard", () => {
  it("shows title, domain, weight and opens summary + external link", () => {
    render(<ReportCitationCard citation={citation} source={live} />);
    const link = screen.getByRole("link", { name: /Milvus benchmarks/i });
    expect(link).toHaveAttribute("href", live.url);
    expect(link).toHaveAttribute("target", "_blank");
    expect(screen.getByText("milvus.io")).toBeInTheDocument();
    expect(screen.getByText("0.90")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Show summary" }));
    expect(screen.getByTestId("research-citation-summary")).toHaveTextContent(
      "Official Milvus ANN benchmark numbers",
    );
    expect(screen.getByTestId("research-citation-card")).toHaveAttribute(
      "data-degraded",
      "false",
    );
  });

  it("renders a degraded placeholder when fetch failed", () => {
    const failed: ResearchSource = {
      ...live,
      id: "src-fail",
      title: "",
      payload: { fetch_failed: true },
    };
    render(
      <ReportCitationCard
        citation={{ ...citation, id: "c2", source_id: "src-fail", label: "[2]" }}
        source={failed}
      />,
    );
    expect(screen.getByText("Source unavailable")).toBeInTheDocument();
    expect(screen.getByText("Fetch failed")).toBeInTheDocument();
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByTestId("research-citation-card")).toHaveAttribute(
      "data-degraded",
      "true",
    );
    fireEvent.click(screen.getByRole("button", { name: "Show summary" }));
    expect(screen.getByTestId("research-citation-summary")).toHaveTextContent(
      "Could not fetch this source.",
    );
  });
});

describe("ReportCitationList", () => {
  it("resolves live sources and falls back to degraded for orphans", () => {
    render(
      <ReportCitationList
        citations={[
          citation,
          { id: "c-missing", index: 9, source_id: "gone", label: "[9]" },
        ]}
        liveSources={[live]}
      />,
    );
    expect(screen.getAllByTestId("research-citation-card")).toHaveLength(2);
    expect(screen.getByTestId("research-citation-list")).toHaveAccessibleName(
      "Citations",
    );
    expect(screen.getByRole("link", { name: /Milvus benchmarks/i })).toBeInTheDocument();
    expect(screen.getByText("Source unavailable")).toBeInTheDocument();
  });
});

describe("LRM-824 citation anchor", () => {
  it("citation number is a locate button that scrolls + flashes its card", () => {
    vi.useFakeTimers();
    const scrollSpy = vi.fn();
    window.HTMLElement.prototype.scrollIntoView = scrollSpy;
    try {
      render(<ReportCitationCard citation={citation} source={live} />);
      const card = screen.getByTestId("research-citation-card");
      expect(card.id).toBe("report-citation-c1");
      const anchor = screen.getByTestId("research-citation-anchor");
      expect(anchor).toHaveAccessibleName("Locate citation [1]");
      fireEvent.click(anchor);
      expect(scrollSpy).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
      expect(card.classList.contains("research-anchor-flash")).toBe(true);
      vi.advanceTimersByTime(1100);
      expect(card.classList.contains("research-anchor-flash")).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });
});
