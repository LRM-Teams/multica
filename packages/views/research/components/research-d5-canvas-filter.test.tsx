import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchCanvasFilter } from "@multica/core/research";
import { ResearchD5CanvasFilter } from "./research-d5-canvas-filter";

const {
  storeState,
  setSessionFilter,
  clearSessionFilter,
  emptyCanvasFilter,
} = vi.hoisted(() => {
  // Inline empty filter — vi.hoisted runs before module imports initialize.
  const emptyCanvasFilter = (): ResearchCanvasFilter => ({
    status: null,
    tier: null,
    round: null,
    cluster: null,
    query: "",
  });
  const storeState: { filterBySession: Record<string, ResearchCanvasFilter> } = {
    filterBySession: { "session-a": emptyCanvasFilter() },
  };
  const setSessionFilter = vi.fn(
    (sessionId: string, partial: Partial<ResearchCanvasFilter>) => {
      storeState.filterBySession[sessionId] = {
        ...(storeState.filterBySession[sessionId] ?? emptyCanvasFilter()),
        ...partial,
      };
    },
  );
  const clearSessionFilter = vi.fn((sessionId: string) => {
    storeState.filterBySession[sessionId] = emptyCanvasFilter();
  });
  return { storeState, setSessionFilter, clearSessionFilter, emptyCanvasFilter };
});

vi.mock("@multica/core/research", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/research")>();
  return {
    ...actual,
    useResearchCanvasStore: (
      selector: (state: {
        filterBySession: typeof storeState.filterBySession;
        setSessionFilter: typeof setSessionFilter;
        clearSessionFilter: typeof clearSessionFilter;
      }) => unknown,
    ) =>
      selector({
        filterBySession: storeState.filterBySession,
        setSessionFilter,
        clearSessionFilter,
      }),
  };
});

describe("ResearchD5CanvasFilter", () => {
  beforeEach(() => {
    storeState.filterBySession = { "session-a": emptyCanvasFilter() };
    setSessionFilter.mockClear();
    clearSessionFilter.mockClear();
  });

  it("writes status filter to the canvas store", () => {
    render(
      <ResearchD5CanvasFilter
        sessionId="session-a"
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

    expect(setSessionFilter).toHaveBeenCalledWith("session-a", {
      status: "running",
    });
    expect(storeState.filterBySession["session-a"]?.status).toBe("running");
  });

  it("clears an active filter", () => {
    storeState.filterBySession["session-a"] = {
      ...emptyCanvasFilter(),
      status: "running",
    };
    render(
      <ResearchD5CanvasFilter
        sessionId="session-a"
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
    expect(clearSessionFilter).toHaveBeenCalledWith("session-a");
    expect(storeState.filterBySession["session-a"]?.status).toBeNull();
  });
});
