// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  canRenderCanvas,
  capabilityFromThrownError,
  classifyV6Probe,
} from "./capability";

describe("classifyV6Probe — capability detection", () => {
  it("V6 route answers with a schema-valid snapshot → v6", () => {
    const verdict = classifyV6Probe({ ok: true });
    expect(verdict).toEqual({ kind: "v6", source: "v6" });
    expect(canRenderCanvas(verdict)).toBe(true);
  });

  it("V6 route 404 → the only permitted fall back to V5", () => {
    const verdict = classifyV6Probe({ ok: false, status: 404 });
    expect(verdict).toEqual({ kind: "fallback-v5", source: "v5" });
    expect(canRenderCanvas(verdict)).toBe(true);
  });

  it("V6 route 501 → fall back to V5 (not yet implemented)", () => {
    const verdict = classifyV6Probe({ ok: false, status: 501 });
    expect(verdict.kind).toBe("fallback-v5");
    expect("source" in verdict && verdict.source).toBe("v5");
  });

  it("V6 200 but schema-valid body with an unknown version → diagnostic, NOT silent data", () => {
    const verdict = classifyV6Probe({
      ok: true,
      unknownVersion: true,
      message: "unrecognised projection version v999",
    });
    expect(verdict.kind).toBe("unknown-version");
    expect(canRenderCanvas(verdict)).toBe(false);
  });

  it("V6 200 but schema mismatch → interface error, never silently fall back to V5", () => {
    const verdict = classifyV6Probe({
      ok: false,
      schemaMismatch: true,
      message: "node.nodes[0].id: expected string, got number",
    });
    expect(verdict.kind).toBe("interface-error");
    expect(canRenderCanvas(verdict)).toBe(false);
    // Interface error must NOT be treated as a V5 fallback.
    expect(verdict).not.toEqual({ kind: "fallback-v5", source: "v5" });
  });

  it("other HTTP statuses (e.g. 500/502) → interface error, not V5 fallback", () => {
    expect(classifyV6Probe({ ok: false, status: 500 }).kind).toBe("interface-error");
    expect(classifyV6Probe({ ok: false, status: 502 }).kind).toBe("interface-error");
    expect(classifyV6Probe({ ok: false, status: 503 }).kind).toBe("interface-error");
  });

  it("only 404/501 degrade; 400 is not a route-absent signal", () => {
    expect(classifyV6Probe({ ok: false, status: 400 }).kind).toBe("interface-error");
  });
});

describe("capabilityFromThrownError — ApiError-like rejection mapping", () => {
  it("maps a 404 ApiError to v5 fallback", () => {
    const err = Object.assign(new Error("route absent"), { status: 404 });
    expect(capabilityFromThrownError(err)).toEqual({ kind: "fallback-v5", source: "v5" });
  });

  it("maps a 501 ApiError to v5 fallback", () => {
    const err = Object.assign(new Error("not implemented"), { status: 501 });
    expect(capabilityFromThrownError(err)).toEqual({ kind: "fallback-v5", source: "v5" });
  });

  it("maps a 500 to interface error", () => {
    const err = Object.assign(new Error("boom"), { status: 500 });
    const verdict = capabilityFromThrownError(err);
    expect(verdict?.kind).toBe("interface-error");
  });

  it("maps a schema parse error (no status) to interface error", () => {
    const err = new Error("node.nodes[0].id: Invalid input: expected string");
    expect(capabilityFromThrownError(err)?.kind).toBe("interface-error");
  });

  it("returns null for an abort (session/version switched mid-probe)", () => {
    const abort = new Error("aborted");
    abort.name = "AbortError";
    expect(capabilityFromThrownError(abort)).toBeNull();
  });

  it("returns null for a null/undefined error", () => {
    expect(capabilityFromThrownError(null)).toBeNull();
  });
});
