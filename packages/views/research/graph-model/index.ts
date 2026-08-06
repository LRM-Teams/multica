/**
 * Unified research-canvas ViewModel (FE-04).
 *
 * Render-layer model over the unified canvas projection produced by
 * `@multica/core/adapters`. Owns display-only state (layout positions, folded
 * node ids) and the idempotent, scoped delta/tombstone reducer. Canonical
 * graph facts are never authored here — they always come from the adapters.
 */
export * from "./types";
export * from "./positioner";
export * from "./model";
