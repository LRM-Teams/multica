/**
 * @vitest-environment jsdom
 */
import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useEnsureMessageLoaded } from "./use-ensure-message-loaded";

describe("useEnsureMessageLoaded", () => {
  it("is idle when there is no target", () => {
    const fetchOlder = vi.fn();
    const { result } = renderHook(() =>
      useEnsureMessageLoaded({
        targetId: null,
        targetLoaded: false,
        hasOlder: true,
        isFetchingOlder: false,
        fetchOlder,
      }),
    );
    expect(result.current).toBe("idle");
    expect(fetchOlder).not.toHaveBeenCalled();
  });

  it("reports found without fetching when the target is already loaded", () => {
    const fetchOlder = vi.fn();
    const { result } = renderHook(() =>
      useEnsureMessageLoaded({
        targetId: "m1",
        targetLoaded: true,
        hasOlder: true,
        isFetchingOlder: false,
        fetchOlder,
      }),
    );
    expect(result.current).toBe("found");
    expect(fetchOlder).not.toHaveBeenCalled();
  });

  it("fetches older pages until the target loads, then reports found", () => {
    const fetchOlder = vi.fn();
    const { result, rerender } = renderHook(
      (props: { targetLoaded: boolean; hasOlder: boolean; isFetchingOlder: boolean }) =>
        useEnsureMessageLoaded({ targetId: "old", fetchOlder, ...props }),
      { initialProps: { targetLoaded: false, hasOlder: true, isFetchingOlder: false } },
    );

    // Not loaded, more pages available → drives one fetch and reports searching.
    expect(result.current).toBe("searching");
    expect(fetchOlder).toHaveBeenCalledTimes(1);

    // While the page is in flight it must not fire another fetch.
    rerender({ targetLoaded: false, hasOlder: true, isFetchingOlder: true });
    expect(fetchOlder).toHaveBeenCalledTimes(1);
    expect(result.current).toBe("searching");

    // Page arrived, still not the target → fetch the next older page.
    rerender({ targetLoaded: false, hasOlder: true, isFetchingOlder: false });
    expect(fetchOlder).toHaveBeenCalledTimes(2);

    // Target finally loaded → found, no further fetch.
    rerender({ targetLoaded: true, hasOlder: true, isFetchingOlder: false });
    expect(result.current).toBe("found");
    expect(fetchOlder).toHaveBeenCalledTimes(2);
  });

  it("reports exhausted when history runs out without finding the target and stops looping", () => {
    const fetchOlder = vi.fn();
    const { result, rerender } = renderHook(
      (props: { hasOlder: boolean }) =>
        useEnsureMessageLoaded({
          targetId: "missing",
          targetLoaded: false,
          isFetchingOlder: false,
          fetchOlder,
          ...props,
        }),
      { initialProps: { hasOlder: false } },
    );

    expect(result.current).toBe("exhausted");
    expect(fetchOlder).not.toHaveBeenCalled();

    // A spurious re-render for the same exhausted target must not re-drive fetch.
    rerender({ hasOlder: false });
    expect(fetchOlder).not.toHaveBeenCalled();
    expect(result.current).toBe("exhausted");
  });

  it("re-drives the search when the target changes to a new unloaded id", () => {
    const fetchOlder = vi.fn();
    const { result, rerender } = renderHook(
      (props: { targetId: string; hasOlder: boolean }) =>
        useEnsureMessageLoaded({
          targetLoaded: false,
          isFetchingOlder: false,
          fetchOlder,
          ...props,
        }),
      { initialProps: { targetId: "a", hasOlder: false } },
    );
    expect(result.current).toBe("exhausted");

    // New target, pages available again → resumes searching + fetches.
    rerender({ targetId: "b", hasOlder: true });
    expect(result.current).toBe("searching");
    expect(fetchOlder).toHaveBeenCalledTimes(1);
  });

  // LRM-1063: cold open / disabled query must not false-exhaust (toast + block
  // later older-page fetches via concludedForRef).
  it("stays searching while isPending even when hasOlder is false", () => {
    const fetchOlder = vi.fn();
    const { result, rerender } = renderHook(
      (props: {
        isPending: boolean;
        hasOlder: boolean;
        targetLoaded: boolean;
      }) =>
        useEnsureMessageLoaded({
          targetId: "m-deep",
          isFetchingOlder: false,
          fetchOlder,
          ...props,
        }),
      {
        initialProps: {
          isPending: true,
          hasOlder: false,
          targetLoaded: false,
        },
      },
    );

    expect(result.current).toBe("searching");
    expect(fetchOlder).not.toHaveBeenCalled();

    // First page arrives without the target, but older pages exist → fetch.
    rerender({ isPending: false, hasOlder: true, targetLoaded: false });
    expect(result.current).toBe("searching");
    expect(fetchOlder).toHaveBeenCalledTimes(1);

    rerender({ isPending: false, hasOlder: true, targetLoaded: true });
    expect(result.current).toBe("found");
  });

  it("does not lock exhausted across a pending→ready transition (LRM-1063)", () => {
    const fetchOlder = vi.fn();
    const { result, rerender } = renderHook(
      (props: { isPending: boolean; hasOlder: boolean }) =>
        useEnsureMessageLoaded({
          targetId: "old-msg",
          targetLoaded: false,
          isFetchingOlder: false,
          fetchOlder,
          ...props,
        }),
      { initialProps: { isPending: true, hasOlder: false } },
    );
    expect(result.current).toBe("searching");

    // Without isPending, this frame would have concluded exhausted and blocked
    // the subsequent hasOlder=true fetch forever.
    rerender({ isPending: false, hasOlder: true });
    expect(result.current).toBe("searching");
    expect(fetchOlder).toHaveBeenCalledTimes(1);
  });
});
