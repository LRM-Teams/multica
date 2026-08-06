/**
 * Unified research-canvas adapters (FE-04).
 *
 * Both the V5 session graph and the V6 graph projection are reduced here to a
 * single render-layer canvas model. The canonical field contract is defined in
 * `canvas-types.ts`; `apply-delta.ts` provides the idempotent, sequence-framed
 * delta application; `v5.ts` / `v6.ts` map their respective backend contracts
 * with no field guessing.
 */
export * from "./canvas-types";
export * from "./snapshot-hash";
export * from "./apply-delta";
export * from "./v5";
export * from "./v6";
export * from "./v6-types";
