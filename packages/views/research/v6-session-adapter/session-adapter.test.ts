// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  v5FixtureGraph,
  v6FixtureSnapshot,
} from "@multica/core/adapters/fixtures";
import { adaptV5Session, adaptV6Session, resolveSessionCanvas } from "./session-adapter";
import { classifyV6Probe } from "./capability";

describe("adaptV5Session — V5 path produces a unified CanvasSnapshot", () => {
  it("maps the V5 fixture graph to a render-layer snapshot", () => {
    const { nodes, edges } = v5FixtureGraph();
    const canvas = adaptV5Session({ sessionId: "session-research-a", nodes, edges });
    expect(canvas.source).toBe("v5");
    expect(canvas.empty).toBe(false);
    expect(canvas.snapshot.nodes).toHaveLength(nodes.length);
    expect(canvas.snapshot.edges).toHaveLength(edges.length);
    // Snapshot id is the stable V5 session-derived id.
    expect(canvas.snapshot.snapshotId).toBe("v5:session-research-a:0");
    // No diagnostics on a clean V5 source.
    expect(canvas.diagnostics).toHaveLength(0);
  });
});

describe("adaptV6Session — V6 path produces a unified CanvasSnapshot", () => {
  it("maps the V6 fixture snapshot, degrading only unknown kinds to generic", () => {
    const snapshot = v6FixtureSnapshot();
    const canvas = adaptV6Session(snapshot);
    expect(canvas.source).toBe("v6");
    // 5 known kinds + 1 unknown_future_kind.
    expect(canvas.snapshot.nodes).toHaveLength(6);
    expect(canvas.snapshot.edges).toHaveLength(6);

    const known = canvas.snapshot.nodes.find((n) => n.id === "run-v6-contract-fixture:question:q1")!;
    expect(known.kind).toBe("question");
    expect(known.id).toBe("run-v6-contract-fixture:question:q1");

    // Unknown future kind degrades to generic (never crashes, never guessed).
    const generic = canvas.snapshot.nodes.find((n) => n.id === "run-v6-contract-fixture:unknown_future_kind:u1")!;
    expect(generic.kind).toBe("generic");
    // And a diagnostic is surfaced so a caller can render generic + a note.
    expect(canvas.diagnostics.some((d) => d.ownerId === generic.id)).toBe(true);
  });

  it("propagates the canonical snapshot id and sequence", () => {
    const canvas = adaptV6Session(v6FixtureSnapshot());
    expect(canvas.snapshot.snapshotId).toBe("v6-snap-1");
    expect(canvas.snapshot.throughEventSequence).toBe(6);
  });
});

describe("resolveSessionCanvas — honest selection, never silent wrong data", () => {
  it("v6 verdict + V6 snapshot → v6 canvas", () => {
    const verdict = classifyV6Probe({ ok: true });
    const res = resolveSessionCanvas({ verdict, v6: v6FixtureSnapshot() });
    expect(res.state).toBe("ok");
    if (res.state === "ok") expect(res.canvas.source).toBe("v6");
  });

  it("fallback-v5 verdict + V5 graph → v5 canvas", () => {
    const verdict = classifyV6Probe({ ok: false, status: 404 });
    const { nodes, edges } = v5FixtureGraph();
    const res = resolveSessionCanvas({
      verdict,
      v5: { sessionId: "s1", nodes, edges },
    });
    expect(res.state).toBe("ok");
    if (res.state === "ok") expect(res.canvas.source).toBe("v5");
  });

  it("interface-error verdict → error, NOT a silent V5 fallback", () => {
    const verdict = classifyV6Probe({ ok: false, schemaMismatch: true });
    const res = resolveSessionCanvas({ verdict, v5: { sessionId: "s1", nodes: [], edges: [] } });
    expect(res.state).toBe("error");
    if (res.state === "error") expect(res.kind).toBe("interface-error");
  });

  it("unknown-version verdict → error (diagnostic), not fabricated data", () => {
    const verdict = classifyV6Probe({ ok: true, unknownVersion: true });
    const res = resolveSessionCanvas({ verdict });
    expect(res.state).toBe("error");
    if (res.state === "error") expect(res.kind).toBe("unknown-version");
  });

  it("v6 verdict without a v6 snapshot → interface error, never fabricate", () => {
    const verdict = classifyV6Probe({ ok: true });
    const res = resolveSessionCanvas({ verdict });
    expect(res.state).toBe("error");
    if (res.state === "error") expect(res.kind).toBe("interface-error");
  });
});
