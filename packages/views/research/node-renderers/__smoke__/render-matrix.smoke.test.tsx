/**
 * LRM-1475 AC1/AC2 — fixture-driven render matrix: all known kinds + all 8 states
 * render without crashing, and unknown kinds degrade to generic.
 */
import { describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/react";
import type { ResearchV6UnknownKindDiagnostic } from "@multica/core/types/research-v6";
import { NodeRenderer } from "../node-renderer";
import { NodeCardShell } from "../node-card-shell";
import { UI01_FIXTURE_NODES, UI01_STATE_NODES } from "../__fixtures__/ui01-contract-fixture";
import { NODE_CARD_STATES } from "../node-state-matrix";
import { KNOWN_NODE_KINDS, NODE_KIND_FAMILIES } from "../node-kind-registry";

describe("render matrix — all known kinds (AC1)", () => {
  it("every fixture node (known kinds + 1 unknown) renders without throwing", () => {
    for (const node of UI01_FIXTURE_NODES) {
      const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
      expect(() =>
        render(<NodeRenderer node={node} diagnostics={diagnostics} />),
      ).not.toThrow();
      cleanup();
    }
  });

  it("all known kinds render known cards, unknown renders generic card", () => {
    const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
    const known = UI01_FIXTURE_NODES.filter((n) => n.node_kind !== "some_future_kind");
    const unknown = UI01_FIXTURE_NODES.find((n) => n.node_kind === "some_future_kind")!;

    expect(known).toHaveLength(KNOWN_NODE_KINDS.length);
    for (const node of known) {
      const { queryByTestId } = render(<NodeRenderer node={node} diagnostics={diagnostics} />);
      expect(queryByTestId("generic-node-card")).toBeNull();
      cleanup();
    }

    // unknown → generic
    const { queryByTestId, getByTestId } = render(<NodeRenderer node={unknown} diagnostics={diagnostics} />);
    expect(queryByTestId("node-card")).toBeNull();
    expect(getByTestId("generic-node-card")).toBeTruthy();
    cleanup();
  });
});

describe("render matrix — 8 states × every family (AC2)", () => {
  it("8 states × every family renders without crashing", () => {
    for (const family of NODE_KIND_FAMILIES) {
      for (const state of NODE_CARD_STATES) {
        expect(() =>
          render(
            <NodeCardShell
              family={family}
              state={state}
              title={`${family}-${state}`}
              typeLabel={family}
              summary="矩阵行摘要"
              importance={2}
            />,
          ),
        ).not.toThrow();
        cleanup();
      }
    }
  });

  it("the state fixture renders all 8 sample nodes as cards without crashing", () => {
    const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
    for (const node of UI01_STATE_NODES) {
      const isKnown = node.node_kind !== "some_future_kind";
      const { container } = render(<NodeRenderer node={node} diagnostics={diagnostics} />);
      if (isKnown) {
        expect(container.querySelector('[data-testid="node-card"]')).toBeTruthy();
      } else {
        expect(container.querySelector('[data-testid="generic-node-card"]')).toBeTruthy();
      }
      cleanup();
    }
  });
});
