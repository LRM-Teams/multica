"use client";

/**
 * Research Session V5/V6 data-entry adapter (LRM-1484 / FE-08).
 *
 * Exposes capability detection, the unified V5/V6 → CanvasSnapshot adapter, and
 * the session-scoped React Query hook. Production renderers consume a single
 * `ResearchSessionCanvas` / `CanvasSnapshot`; they never read V5/V6 wire shapes.
 */
export * from "./capability";
export * from "./session-adapter";
export * from "./director-session-adapter";
export * from "./use-research-session-canvas";
