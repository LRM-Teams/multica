"use client";

/**
 * FE-10 / LRM-1486 — Research canvas plugin shell.
 *
 * Typed registration seams + lazy/error isolation + URL deep-link adapter for
 * the six canvas display modules (node renderer, Insight, Dispute, motion,
 * execution overlay, trajectory jump). This package is the single common
 * wiring point; downstream plugin slices register here instead of editing
 * research-canvas.tsx.
 *
 * See `types.ts` for the display-only contract, `registry.ts` for the typed
 * registry, `plugin-host.tsx` for per-slot lazy + error isolation,
 * `fallbacks.tsx` for the business-free loading/error/absent states,
 * `plugin-shell.tsx` for the one canvas composition seam, and `url-state.ts`
 * for the lens/node/view deep-link adapter (History API only, no router).
 */

export * from "./types";
export * from "./context";
export * from "./registry";
export * from "./fallbacks";
export * from "./plugin-host";
export * from "./plugin-slots";
export * from "./plugin-shell";
export * from "./use-registered-slot";
export * from "./use-node-renderers";
export * from "./url-state";
export * from "./use-canvas-url-state";
