import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import {
  GenericNode,
  V6NodeView,
  toV6NodeViewModel,
  toV6EdgeViewModel,
} from "./node-view";
import type { ResearchV6UnknownKindDiagnostic } from "@multica/core/types/research-v6";

afterEach(() => cleanup());

function diagnostics(): ResearchV6UnknownKindDiagnostic[] {
  return [];
}

describe("V6 node render surface (AC #2: GenericNode, no crash)", () => {
  it("renders a known node kind through the registry", () => {
    const diags = diagnostics();
    const vm = toV6NodeViewModel(
      {
        id: "run-1:task:t1",
        node_kind: "task",
        title: "调查供应商 A",
        summary: "边界",
        status: "running",
        run_id: "run-1",
      },
      diags,
    );
    expect(vm.isGeneric).toBe(false);
    render(<V6NodeView viewModel={vm} />);
    expect(screen.getByTestId("v6-known-node")).toBeTruthy();
    expect(screen.getByTestId("v6-known-node").getAttribute("data-kind")).toBe("task");
    expect(diags).toHaveLength(0);
  });

  it("renders an unknown node kind as GenericNode with a recorded diagnostic", () => {
    const diags = diagnostics();
    const vm = toV6NodeViewModel(
      {
        id: "run-1:future_quantum_insight:x7",
        node_kind: "future_quantum_insight",
        title: "量子洞察",
        summary: "未知",
        status: "pending",
        run_id: "run-1",
      },
      diags,
    );
    expect(vm.isGeneric).toBe(true);
    if (vm.isGeneric) {
      expect(vm.diagnostic.raw).toBe("future_quantum_insight");
      expect(vm.diagnostic.owner_id).toBe("run-1:future_quantum_insight:x7");
    }
    render(<V6NodeView viewModel={vm} />);
    // GenericNode rendered, page did not crash
    expect(screen.getByTestId("v6-generic-node")).toBeTruthy();
    expect(screen.getByTestId("v6-generic-node").getAttribute("data-kind")).toBe("future_quantum_insight");
    expect(diags).toHaveLength(1);
  });

  it("GenericNode exposes the raw kind inside the diagnostics detail", () => {
    const diags = diagnostics();
    const vm = toV6NodeViewModel(
      {
        id: "run-1:weird_kind:z",
        node_kind: "weird_kind",
        title: "",
        summary: "",
        status: "",
        run_id: "run-1",
      },
      diags,
    );
    if (!vm.isGeneric) throw new Error("expected generic");
    render(<GenericNode viewModel={vm} />);
    expect(screen.getByTestId("v6-diagnostic-raw").textContent).toBe("weird_kind");
  });

  it("rendering many unknown kinds never throws and records one diagnostic each", () => {
    const diags = diagnostics();
    const kinds = ["a", "b_2", "future_kind3", "", "!!!"];
    for (let i = 0; i < kinds.length; i++) {
      const vm = toV6NodeViewModel(
        {
          id: `run-1:${kinds[i]}:n${i}`,
          node_kind: kinds[i]!,
          title: `t${i}`,
          summary: "",
          status: "",
          run_id: "run-1",
        },
        diags,
      );
      expect(vm.isGeneric).toBe(true);
      render(<V6NodeView viewModel={vm} />);
      // every rendered node is a GenericNode (page never crashes), count grows
      expect(screen.getAllByTestId("v6-generic-node")).toHaveLength(i + 1);
    }
    expect(diags).toHaveLength(kinds.length);
  });

  it("classifies unknown edge types to generic without throwing", () => {
    const diags = diagnostics();
    const edge = toV6EdgeViewModel({ id: "e1", edge_type: "teleports", run_id: "run-1" }, diags);
    expect(edge.isGeneric).toBe(true);
    expect(edge.label).toBe("未知关系");
    expect(diags).toHaveLength(1);
  });

  it("classifies known edge types to a non-generic relation", () => {
    const diags = diagnostics();
    const edge = toV6EdgeViewModel({ id: "e1", edge_type: "depends_on", run_id: "run-1" }, diags);
    expect(edge.isGeneric).toBe(false);
    expect(diags).toHaveLength(0);
  });
});
