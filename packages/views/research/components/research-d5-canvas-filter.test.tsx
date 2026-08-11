import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { emptyCanvasFilter, type ResearchCanvasFilter } from "@multica/core/research";
import { ResearchD5CanvasFilter } from "./research-d5-canvas-filter";

const { storeState, setFilter, clearFilter } = vi.hoisted(() => {
  const storeState: { filter: ResearchCanvasFilter } = {
    filter: emptyCanvasFilter(),
  };
  const setFilter = vi.fn((partial: Partial<ResearchCanvasFilter>) => {
    storeState.filter = { ...storeState.filter, ...partial };
  });
  const clearFilter = vi.fn(() => {
    storeState.filter = emptyCanvasFilter();
  });
  return { storeState, setFilter, clearFilter };
});

vi.mock("@multica/core/research", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/research")>();
  return {
    ...actual,
    useResearchCanvasStore: (
      selector: (state: {
        filter: typeof storeState.filter;
        setFilter: typeof setFilter;
        clearFilter: typeof clearFilter;
      }) => unknown,
    ) =>
      selector({
        filter: storeState.filter,
        setFilter,
        clearFilter,
      }),
  };
});

describe("ResearchD5CanvasFilter", () => {
  beforeEach(() => {
    storeState.filter = emptyCanvasFilter();
    setFilter.mockClear();
    clearFilter.mockClear();
  });

  it("writes status filter to the canvas store", () => {
    render(
      <ResearchD5CanvasFilter
        options={{
          statuses: ["running", "done"],
          tiers: [],
          rounds: [],
          clusters: [],
        }}
      />,
    );

    fireEvent.click(screen.getByTestId("research-d5-filter-trigger"));
    fireEvent.change(screen.getByTestId("research-d5-filter-status"), {
      target: { value: "running" },
    });

    expect(setFilter).toHaveBeenCalledWith({ status: "running" });
    expect(storeState.filter.status).toBe("running");
  });

  it("clears an active filter", () => {
    storeState.filter = { ...emptyCanvasFilter(), status: "running" };
    render(
      <ResearchD5CanvasFilter
        options={{
          statuses: ["running"],
          tiers: [],
          rounds: [],
          clusters: [],
        }}
      />,
    );

    fireEvent.click(screen.getByTestId("research-d5-filter-trigger"));
    fireEvent.click(screen.getByTestId("research-d5-filter-clear"));
    expect(clearFilter).toHaveBeenCalled();
    expect(storeState.filter.status).toBeNull();
  });
});
