"use client";

/**
 * Research Session V5/V6 capability detection (LRM-1484 / FE-08).
 *
 * The live canvas must choose a data entry based on a *verifiable* backend
 * capability, never by guessing. This module classifies the outcome of probing
 * the V6 projection route into one of four well-defined verdicts:
 *
 *   - `v6`            — the V6 route answered with a schema-valid snapshot.
 *   - `fallback-v5`   — the V6 route is explicitly absent (404) or not yet
 *                       implemented (501). The client may use the verified V5
 *                       adapter. This is the ONLY permitted capability degrade.
 *   - `interface-error` — the V6 route answered 2xx but the payload failed
 *                       schema validation. This is a backend/frontend contract
 *                       mismatch, NOT a missing route: we must surface it, never
 *                       silently fall back to V5 and paint a different graph.
 *   - `unknown-version` — the payload parsed but declares a version the client
 *                       cannot reason about. We degrade to a diagnostic
 *                       (never to fabricated research data).
 *
 * Rule (data-contract §2): V6 404/501 are the only cases that demote to V5;
 * a 200-with-bad-schema must be reported as an interface error.
 */
import type { CanvasSnapshot } from "@multica/core/adapters";

/** V6 none/404/501 are the only cases allowed to fall back to the V5 adapter. */
const V5_FALLBACK_HTTP_STATUSES = new Set([404, 501]);

export type ResearchSource = "v5" | "v6";

export type CapabilityVerdict =
  | { kind: "v6"; source: "v6" }
  | { kind: "fallback-v5"; source: "v5" }
  | { kind: "interface-error"; reason: string }
  | { kind: "unknown-version"; reason: string };

/**
 * Classify the outcome of a V6 snapshot probe into a CapabilityVerdict.
 *
 * The `error` argument is the raw rejection from the V6 load call:
 *   - an object carrying an HTTP status (ApiError-like) → route semantics;
 *   - a schema-mismatch error (NOT an HTTP status) after a 2xx → interface
 *     error (a 200 that failed to parse is a contract bug, not a missing route);
 *   - any other outcome → interface error / unknown.
 *
 * Pure and synchronous: callers pass the concrete outcome values, so this
 * function is trivially unit-testable with 404/501/200-schema-error/unknown
 * fixtures without any network.
 */
export function classifyV6Probe(args: {
  ok: boolean;
  /** HTTP status when the probe got a non-2xx; undefined on schema/other error. */
  status?: number;
  /** True when the probe returned 2xx but the body failed schema parse. */
  schemaMismatch?: boolean;
  /** True when the payload parsed but carried an unrecognised version. */
  unknownVersion?: boolean;
  message?: string;
}): CapabilityVerdict {
  const { ok, status, schemaMismatch, unknownVersion, message = "" } = args;

  if (ok) {
    if (unknownVersion) {
      return {
        kind: "unknown-version",
        reason: message || "V6 snapshot parsed but its version is not recognised",
      };
    }
    return { kind: "v6", source: "v6" };
  }

  // A 2xx body that failed schema validation is an interface error — the route
  // exists but the contract is wrong. Never demote to V5 here.
  if (schemaMismatch) {
    return {
      kind: "interface-error",
      reason: message || "V6 route returned a payload that fails schema validation",
    };
  }

  // Only 404/501 are allowed to demote to the verified V5 adapter.
  if (status != null && V5_FALLBACK_HTTP_STATUSES.has(status)) {
    return { kind: "fallback-v5", source: "v5" };
  }

  return {
    kind: "interface-error",
    reason:
      status != null
        ? `V6 probe failed with HTTP ${status}${message ? `: ${message}` : ""}`
        : message || "V6 probe failed for an unknown reason",
  };
}

/**
 * Derive a CapabilityVerdict from a thrown probe error (ApiError-like).
 * Handles the abort case (session/version switched mid-probe) as a no-verdict.
 */
export function capabilityFromThrownError(error: unknown): CapabilityVerdict | null {
  if (error == null) return null;
  if (error instanceof Error && error.name === "AbortError") return null;
  const maybe = error as { status?: unknown; message?: unknown };
  if (typeof maybe.status === "number") {
    return classifyV6Probe({
      ok: false,
      status: maybe.status,
      message: typeof maybe.message === "string" ? maybe.message : undefined,
    });
  }
  return classifyV6Probe({
    ok: false,
    schemaMismatch: true,
    message: error instanceof Error ? error.message : String(error),
  });
}

/**
 * True when a capability verdict means the unified snapshot body may be built
 * with real research data (either the V5 adapter or the V6 adapter).
 */
export function canRenderCanvas(verdict: CapabilityVerdict): verdict is
  | { kind: "v6"; source: "v6" }
  | { kind: "fallback-v5"; source: "v5" } {
  return verdict.kind === "v6" || verdict.kind === "fallback-v5";
}

/** The source an adapter verdict resolves to (V5 or V6), or null when in error. */
export function sourceOfVerdict(
  verdict: CapabilityVerdict | null | undefined,
): ResearchSource | null {
  if (!verdict) return null;
  return verdict.kind === "v6" || verdict.kind === "fallback-v5"
    ? verdict.source
    : null;
}

/** Empty, stable snapshot used before any source has produced data. */
export function emptyCanvasSnapshot(): CanvasSnapshot {
  return {
    snapshotId: "",
    throughEventSequence: 0,
    graphContentHash: "",
    nodes: [],
    edges: [],
  };
}
