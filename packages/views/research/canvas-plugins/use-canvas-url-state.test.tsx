import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useCanvasUrlState, type CanvasUrlHistory } from "./use-canvas-url-state";

afterEach(() => cleanup());

/** In-memory History API fake: records pushes, lets tests trigger Back/Forward. */
function createFakeHistory(initialSearch = ""): {
  history: CanvasUrlHistory;
  recorded: string[];
  search: () => string;
  back: () => void;
} {
  let search = initialSearch;
  const listeners = new Set<() => void>();
  const recorded: string[] = [];
  return {
    history: {
      getSearch: () => search,
      getPathname: () => "/research/run-1",
      push: (s) => {
        search = s;
        recorded.push(s);
      },
      replace: (s) => {
        search = s;
      },
      subscribe: (listener) => {
        listeners.add(listener);
        return () => listeners.delete(listener);
      },
    },
    recorded,
    search: () => search,
    back: () => {
      // Simulate popstate: re-read whatever the fake currently holds.
      for (const l of listeners) l();
    },
  };
}

describe("useCanvasUrlState — FE-10 AC (deep-link + Back/Forward, no router)", () => {
  it("initializes from a deep-link query", () => {
    const fake = createFakeHistory("?lens=execution&node=run-1:node:1");
    const { result } = renderHook(() => useCanvasUrlState({ history: fake.history }));
    expect(result.current.state).toEqual({
      lens: "execution",
      node: "run-1:node:1",
    });
  });

  it("pushes lens/node/view to the URL and preserves unrelated params", () => {
    const fake = createFakeHistory("?workspace=abc");
    const { result } = renderHook(() => useCanvasUrlState({ history: fake.history }));
    act(() => {
      result.current.set({ lens: "insight", node: "run-1:node:9", view: "trajectory" });
    });
    expect(fake.search()).toContain("lens=insight");
    expect(fake.search()).toContain("node=run-1%3Anode%3A9");
    expect(fake.search()).toContain("view=trajectory");
    expect(fake.search()).toContain("workspace=abc");
    // Only one meaningful push happened (no redundant duplicate).
    expect(fake.recorded.length).toBe(1);
  });

  it("setting only node keeps the current lens (merge semantics)", () => {
    const fake = createFakeHistory("?lens=execution&node=n1");
    const { result } = renderHook(() => useCanvasUrlState({ history: fake.history }));
    act(() => {
      result.current.set({ node: "n2" });
    });
    expect(result.current.state).toEqual({ lens: "execution", node: "n2" });
    expect(fake.search()).toContain("lens=execution");
    expect(fake.search()).toContain("node=n2");
  });

  it("does not push when the deep-link state is unchanged", () => {
    const fake = createFakeHistory("?lens=execution&node=n1");
    const { result } = renderHook(() => useCanvasUrlState({ history: fake.history }));
    act(() => {
      result.current.set({ lens: "execution", node: "n1" });
    });
    expect(fake.recorded.length).toBe(0);
  });

  it("follows Back/Forward via popstate by re-reading the URL", () => {
    const fake = createFakeHistory("?lens=execution&node=n1");
    const { result } = renderHook(() => useCanvasUrlState({ history: fake.history }));
    act(() => {
      result.current.set({ node: "n2" });
    });
    expect(result.current.state.node).toBe("n2");
    // Simulate user pressing Back (URL changes + popstate fires).
    fake.history.replace("?lens=execution&node=n1", "");
    act(() => {
      fake.back();
    });
    expect(result.current.state.node).toBe("n1");
    // Forward.
    fake.history.replace("?lens=execution&node=n2", "");
    act(() => {
      fake.back();
    });
    expect(result.current.state.node).toBe("n2");
  });

  it("clearing a field via empty string removes it from URL state", () => {
    const fake = createFakeHistory("?lens=execution&node=n1");
    const { result } = renderHook(() => useCanvasUrlState({ history: fake.history }));
    act(() => {
      result.current.set({ node: "" });
    });
    expect(result.current.state.node).toBeUndefined();
    expect(fake.search()).not.toContain("node=");
  });
});
