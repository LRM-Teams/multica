// @vitest-environment jsdom
/**
 * LRM-1477 — useSemanticTransition React integration tests.
 *
 * Verifies the hook maps queued projection deltas to per-entity DOM directives
 * and persistent static markers, honours settleNow and the reduced-motion /
 * low-performance profiles, and exposes the live queue size. The pure state
 * machine is covered by transition-queue.test.ts; here we lock the React
 * binding (enqueue → directiveFor / markerFor) with a controlled clock.
 */
import { renderHook, act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSemanticTransition } from "./use-semantic-transition";

beforeEach(() => {
  // Deterministic clock for reducer bookkeeping.
  vi.stubGlobal("performance", { now: () => 1000 });
  // Swallow the RAF loop so the test is not driven by wall-clock frames.
  window.requestAnimationFrame = (() => 0) as unknown as typeof requestAnimationFrame;
  window.cancelAnimationFrame = (() => {}) as unknown as typeof cancelAnimationFrame;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useSemanticTransition — enqueue → directive/marker binding", () => {
  it("exposes a per-entity directive for a live enqueued conflict", () => {
    const { result } = renderHook(() => useSemanticTransition());
    act(() => {
      result.current.enqueue({
        transition_kind: "dispute_opened",
        related_ids: ["claim-1"],
        anchor_id: "claim-1",
      });
    });
    const d = result.current.directiveFor("claim-1");
    expect(d).not.toBeNull();
    expect(d!.dataVerb).toBe("conflict");
    expect(d!.className).toContain("research-motion-conflict");
    expect(d!.style.animationName).toBe("research-motion-conflict");
  });

  it("keeps the persistent conflict marker while the entry is live (Rule ②)", () => {
    const { result } = renderHook(() => useSemanticTransition());
    act(() => {
      result.current.enqueue({
        transition_kind: "dispute_opened",
        related_ids: ["claim-2"],
        anchor_id: "claim-2",
      });
    });
    expect(result.current.markerFor("claim-2")).toContain(
      "research-motion-marker-conflict-border",
    );
  });

  it("returns null for entities that were never enqueued", () => {
    const { result } = renderHook(() => useSemanticTransition());
    expect(result.current.directiveFor("ghost")).toBeNull();
    expect(result.current.markerFor("ghost")).toBeNull();
  });
});

describe("useSemanticTransition — settleNow", () => {
  it("settleNow drops all live animation and clears directives", () => {
    const { result } = renderHook(() => useSemanticTransition());
    act(() => {
      result.current.enqueue({
        transition_kind: "branch_spawned",
        related_ids: ["branch-1"],
        anchor_id: null,
      });
    });
    expect(result.current.queueSize).toBeGreaterThan(0);
    act(() => {
      result.current.settleNow();
    });
    expect(result.current.queueSize).toBe(0);
    expect(result.current.directiveFor("branch-1")).toBeNull();
  });
});

describe("useSemanticTransition — profiles", () => {
  it("collapses displacement to the uniform fade under reduced motion (Rule ④)", () => {
    // Emulate the device-side reduced-motion signal so prefers-reduced-motion
    // resolves true even though jsdom's default matchMedia reports false.
    const nativeMatchMedia = window.matchMedia;
    window.matchMedia = ((query: string) => ({
      matches: query.includes("prefers-reduced-motion: reduce"),
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;

    try {
      const { result } = renderHook(() => useSemanticTransition());
      act(() => {
        result.current.enqueue({
          transition_kind: "lead_escalated",
          related_ids: ["director-1"],
          anchor_id: "director-1",
        });
      });
      expect(result.current.profile.reducedMotion).toBe(true);
      const d = result.current.directiveFor("director-1");
      expect(d!.style.animationName).toBe("research-motion-fade-in");
      // Persistent escalate emphasis marker survives reduced-motion collapse.
      expect(d!.markerClass).toContain("research-motion-marker-escalate-emphasis");
    } finally {
      window.matchMedia = nativeMatchMedia;
    }
  });

  it("exposes the low-performance profile and disables glow", () => {
    const { result } = renderHook(() =>
      useSemanticTransition({ lowPerformance: true }),
    );
    act(() => {
      result.current.enqueue({
        transition_kind: "deliberation_progressed",
        related_ids: ["dis-1"],
        anchor_id: "dis-1",
      });
    });
    expect(result.current.profile.lowPerformance).toBe(true);
    const d = result.current.directiveFor("dis-1");
    expect(d!.glowDisabled).toBe(true);
  });
});
