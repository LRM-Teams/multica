// @vitest-environment jsdom

// LRM-1362 — reduced-motion fallback for `.animate-nav-progress-sweep`.
//
// The class is gated in JS rather than via Tailwind's `motion-reduce:` variant
// on purpose: `.animate-nav-progress-sweep` is a plain class declared in
// `packages/ui/styles/base.css`, which every app entry imports *after*
// `@import "tailwindcss"`. Same specificity, later in the stylesheet — so
// `motion-reduce:animate-none` loses the source-order tie and had no effect at
// all in real Chromium under emulated `reduce`. Gating emission is
// deterministic.

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { NavigationProgress } from "./navigation-progress";

const navigating = vi.hoisted(() => ({ current: true }));

vi.mock("../navigation", () => ({
  useIsNavigating: () => navigating.current,
}));

function setReducedMotion(reduce: boolean) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      matches: reduce && query.includes("prefers-reduced-motion"),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }),
  });
}

describe("NavigationProgress reduced-motion fallback", () => {
  beforeEach(() => {
    navigating.current = true;
  });

  it("sweeps at a third of the width when motion is allowed", () => {
    setReducedMotion(false);
    const { container } = render(<NavigationProgress />);
    const bar = container.querySelector<HTMLElement>("[aria-hidden] > div");

    expect(bar?.className).toContain("animate-nav-progress-sweep");
    expect(bar?.className).toContain("w-1/3");
  });

  it("drops the sweep and goes full width under reduced motion", () => {
    setReducedMotion(true);
    const { container } = render(<NavigationProgress />);
    const bar = container.querySelector<HTMLElement>("[aria-hidden] > div");

    // Motion removed entirely, not slowed.
    expect(bar?.className).not.toContain("animate-nav-progress-sweep");
    // A frozen `w-1/3` bar would read as "33% progress"; this bar is
    // indeterminate, so reduced motion must show it full width.
    expect(bar?.className).not.toContain("w-1/3");
    expect(bar?.className).toContain("w-full");
    // Still the same brand bar — the loading signal itself is not removed.
    expect(bar?.className).toContain("bg-brand");
  });

  it("keeps the bar decorative and unmounts the sweep when idle", () => {
    setReducedMotion(false);
    navigating.current = false;
    const { container } = render(<NavigationProgress />);

    expect(container.querySelector<HTMLElement>("[aria-hidden] > div")).toBeNull();
    expect(container.firstElementChild?.getAttribute("aria-hidden")).toBe("true");
    expect(screen.queryByRole("progressbar")).toBeNull();
  });
});
