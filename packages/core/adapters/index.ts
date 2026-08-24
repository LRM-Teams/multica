/**
 * Unified research-canvas adapters (FE-04).
 *
 * The V5 session graph is reduced to the shared render-layer canvas model.
 * Director V6 owns its separate authoritative projection adapter in views.
 * `canvas-types.ts` defines the render contract and `apply-delta.ts` provides
 * idempotent, sequence-framed updates.
 */
export * from "./canvas-types";
export * from "./snapshot-hash";
export * from "./apply-delta";
export * from "./v5";
