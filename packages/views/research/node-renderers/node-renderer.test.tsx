/**
 * LRM-1475 AC2/AC1 — NodeRenderer renders every one of the 8 states and
 * degrades unknown kinds to GenericNodeCard without crashing.
 */
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ResearchV6UnknownKindDiagnostic } from "@multica/core/types/research-v6";
import { NodeRenderer } from "./node-renderer";
import type { ResearchV6ProjectionNode } from "@multica/core/types/research-v6";

const BASE: ResearchV6ProjectionNode = {
  id: "run:e:1",
  run_id: "run",
  entity_kind: "task",
  entity_id: "1",
  node_kind: "task",
  node_subtype: "",
  schema_version: 1,
  title: "T",
  summary: "S",
  status: "idle",
  importance: 1,
  freshness: null,
  contract_version: "1",
  plan_version: "1",
  strategy_version: "1",
  actor_agent_id: null,
  task_id: null,
  attempt_id: null,
  created_at: null,
  updated_at: null,
  cost: null,
  detail: null,
  created_sequence: 1,
  updated_sequence: 1,
  terminal_sequence: null,
};

function node(status: string, overrides: Partial<ResearchV6ProjectionNode> = {}): ResearchV6ProjectionNode {
  return { ...BASE, status, ...overrides };
}

describe("NodeRenderer — 8 states (AC2)", () => {
  const cases: Array<[string, string, string]> = [
    ["default", "idle", "default"],
    ["running", "running", "running"],
    ["failed", "failed", "failed"],
    ["stale", "stale", "stale"],
    ["loading", "pending", "loading"],
    ["terminal", "done", "terminal"],
  ];

  it.each(cases)("renders %s card from status=%s with data-state=%s", (_label, status, expected) => {
    const { container } = render(<NodeRenderer node={node(status)} />);
    const card = container.querySelector('[data-testid="node-card"]');
    expect(card).toBeTruthy();
    expect(card?.getAttribute("data-state")).toBe(expected);
  });

  it("selected state surfaces the ring highlight", () => {
    const { container } = render(<NodeRenderer node={node("idle")} overriddenState="selected" />);
    const card = container.querySelector('[data-testid="node-card"]');
    expect(card?.getAttribute("data-state")).toBe("selected");
  });

  it("unknown state renders through the generic degradation card", () => {
    const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
    const { container } = render(
      <NodeRenderer node={node("pending", { node_kind: "future_type" })} diagnostics={diagnostics} />,
    );
    const generic = container.querySelector('[data-testid="generic-node-card"]');
    expect(generic).toBeTruthy();
    expect(generic?.getAttribute("data-state")).toBe("unknown");
  });

  it("loading/running mark the node busy (aria-busy)", () => {
    const { container } = render(<NodeRenderer node={node("pending")} />);
    expect(container.querySelector('[aria-busy="true"]')).toBeTruthy();
    const b2 = render(<NodeRenderer node={node("running")} />).container;
    expect(b2.querySelector('[aria-busy="true"]')).toBeTruthy();
  });
});

describe("NodeRenderer — generic degradation (AC1)", () => {
  it("unknown kind renders GenericNodeCard without throwing", () => {
    const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
    const { container } = render(
      <NodeRenderer node={node("pending", { node_kind: "some_future_kind", title: "未来的节点" })} diagnostics={diagnostics} />,
    );
    expect(screen.getByTestId("generic-node-card")).toBeTruthy();
    expect(screen.getByText("未来的节点")).toBeTruthy();
    expect(container.querySelector('[data-testid="node-card"]')).toBeNull();
  });

  it("generic card is a keyboard-activatable native button when onOpen is provided", () => {
    const diagnostics: ResearchV6UnknownKindDiagnostic[] = [];
    const onOpen = vi.fn();
    const { container } = render(
      <NodeRenderer
        node={node("pending", { node_kind: "some_future_kind" })}
        diagnostics={diagnostics}
        onOpen={onOpen}
      />,
    );
    const generic = container.querySelector('[data-testid="generic-node-card"]') as HTMLElement;
    expect(generic.tagName).toBe("BUTTON");
    // Native <button> gives free keyboard/SR activation (Enter/Space → click).
    fireEvent.click(generic);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });
});

describe("NodeRenderer — footer meta", () => {
  it("shows attempt chip and evidence count when present", () => {
    render(
      <NodeRenderer
        node={node("done", {
          attempt_id: "attempt:3",
          detail: { evidence_count: 5 },
        })}
      />,
    );
    expect(screen.getByTestId("node-meta")).toBeTruthy();
    expect(screen.getByTestId("node-evidence-count")).toBeTruthy();
    expect(screen.getByText("5")).toBeTruthy();
  });

  it("shows 'no evidence' muted style for zero evidence", () => {
    render(<NodeRenderer node={node("done", { detail: { evidence_count: 0 } })} />);
    const el = screen.getByTestId("node-evidence-count");
    expect(el.classList.contains("opacity-60")).toBe(true);
  });
});
