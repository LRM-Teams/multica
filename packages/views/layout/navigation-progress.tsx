"use client";

import { useEffect, useState } from "react";

import { useIsNavigating } from "../navigation";
import { usePrefersReducedMotion } from "../common/use-prefers-reduced-motion";

// 2px top-of-content progress bar shown while a transition-wrapped
// push/replace is mid-flight. Indeterminate by design — we don't know
// when the next route will commit, just that it's coming.
//
// The container stays mounted so it can fade out over 200ms instead of
// vanishing in one frame. The inner sweep is mounted only while navigating
// (plus the fade-out tail); leaving its `infinite` keyframe animation
// running while hidden would burn paints on every dashboard view.
export function NavigationProgress() {
  const isNavigating = useIsNavigating();
  const [renderSweep, setRenderSweep] = useState(false);
  const prefersReducedMotion = usePrefersReducedMotion();

  useEffect(() => {
    if (isNavigating) setRenderSweep(true);
  }, [isNavigating]);

  return (
    <div
      aria-hidden
      data-visible={isNavigating ? "true" : "false"}
      onTransitionEnd={(event) => {
        if (event.propertyName === "opacity" && !isNavigating) {
          setRenderSweep(false);
        }
      }}
      className="pointer-events-none absolute inset-x-0 top-0 z-50 h-0.5 overflow-hidden opacity-0 transition-opacity duration-200 data-[visible=true]:opacity-100"
    >
      {renderSweep && (
        <div
          // LRM-1362 — under prefers-reduced-motion the infinite translateX
          // sweep is dropped (WCAG 2.2.2 / 2.3.3). It must not merely freeze at
          // `w-1/3`: a motionless third-width bar reads as "33% done", and this
          // bar is indeterminate by design. Show a static full-width brand bar
          // instead — still says "loading, progress unknown", with zero
          // movement. GitHub and Linear degrade indeterminate bars the same way.
          className={
            prefersReducedMotion
              ? "h-full w-full bg-brand"
              : "h-full w-1/3 animate-nav-progress-sweep bg-brand"
          }
          style={{
            boxShadow:
              "0 0 8px color-mix(in oklab, var(--brand) 60%, transparent), 0 0 2px color-mix(in oklab, var(--brand) 80%, transparent)",
          }}
        />
      )}
    </div>
  );
}
