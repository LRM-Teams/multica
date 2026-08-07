/**
 * Semantic LOD — independent decision layer (LRM-1488).
 *
 * Self-contained seam over the V6 research route canvas, per
 * docs/superpowers/specs/2026-08-06-research-live-canvas-*
 *   - route-topology / node-card / viewport-performance specs.
 *
 * It owns only decision logic — the selector, the budget model, the
 * classification registry, the safe-centre camera intent and the
 * promotion/focus hook. It does NOT touch main-canvas wiring, path geometry,
 * node renderers or motion.
 */
export * from "./model";
export * from "./classify";
export * from "./budget";
export * from "./selector";
export * from "./camera";
export * from "./use-semantic-lod";
export * from "./use-semantic-focus";
