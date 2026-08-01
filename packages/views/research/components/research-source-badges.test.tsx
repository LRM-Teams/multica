import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchSource } from "@multica/core/types";
import { ResearchSourceBadges } from "./research-source-badges";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const value = fn({
        panel: {
          sources: "Sources",
          sources_empty: "No sources",
          sources_hint: "Sorted",
          sources_view_all: "View all · {{count}}",
          sources_collapse: "Collapse",
        },
      });
      if (typeof value === "string" && vars) {
        return value.replace(/\{\{(\w+)\}\}/g, (_, k: string) => String(vars[k] ?? ""));
      }
      return value;
    },
  }),
}));

function source(i: number): ResearchSource {
  return {
    id: `s${i}`,
    session_id: "sess",
    url: `https://example.com/${i}`,
    title: `Source ${i}`,
    source_class: "web",
    credibility_weight: 1 - i * 0.01,
    summary: "",
    excerpt: "",
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
  } as ResearchSource;
}

describe("ResearchSourceBadges (LRM-800)", () => {
  it("truncates then expands to show all sources", () => {
    const sources = Array.from({ length: 15 }, (_, i) => source(i));
    render(<ResearchSourceBadges sources={sources} embedded />);

    expect(screen.getByText("Source 0")).toBeTruthy();
    expect(screen.queryByText("Source 14")).toBeNull();

    const expand = screen.getByTestId("research-sources-expand");
    expect(expand.textContent).toMatch(/View all/);
    fireEvent.click(expand);

    expect(screen.getByText("Source 14")).toBeTruthy();
    expect(screen.getByTestId("research-sources-expand").textContent).toMatch(/Collapse/);
  });

  it("hides expand when under the preview limit", () => {
    render(<ResearchSourceBadges sources={[source(0), source(1)]} embedded />);
    expect(screen.queryByTestId("research-sources-expand")).toBeNull();
  });
});
