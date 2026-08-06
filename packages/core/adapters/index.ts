/**
 * Unified research-canvas adapters (FE-04).
 *
 * Both the V5 session graph and the V6 graph projection are reduced here to a
 * single render-layer canvas model. The canonical field contract is defined in
 * `canvas-types.ts`; `apply-delta.ts` provides the idempotent, sequence-framed
 * delta application; `v5.ts` / `v6.ts` map their respective backend contracts
 * with no field guessing.
 *
 * The V6 wire contract is NOT duplicated here — it is the merged canonical
 * `packages/core/types/research-v6.ts`; the node-kind registry comes from the
 * merged `packages/core/research-v6` registry. `v6.ts` consumes those as the
 * source of truth.
 */
export * from "./canvas-types";
export * from "./snapshot-hash";
export * from "./apply-delta";
export * from "./v5";
export * from "./v6";
