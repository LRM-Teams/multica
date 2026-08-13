// @vitest-environment jsdom
import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { ResearchGraphNode } from "@multica/core/types";
import { ringActionsForNode, SYSTEM_NODE_TYPES } from "../lib/node-action-ring";
import { ResearchNodeActionRing } from "./research-node-action-ring";

import enResearch from "../../locales/en/research.json";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (sel: (x: typeof enResearch) => string) => sel(enResearch),
  }),
}));

const base = {
  id: "n1",
  session_id: "s1",
  title: "Blocked path",
  summary: "No sources",
  status: "abandoned",
  actor_agent_id: null,
  payload: {},
  created_at: "2026-07-31T00:00:00Z",
  updated_at: "2026-07-31T00:00:00Z",
} as unknown as ResearchGraphNode;
const taskBound = { ...base, payload: { task_id: "task-1" } };

describe("ringActionsForNode (LRM-848 / LRM-981)", () => {
  it("makes retry primary on dead_end", () => {
    const actions = ringActionsForNode({ ...taskBound, node_type: "dead_end" });
    expect(actions[0]).toMatchObject({ id: "retry", primary: true });
    expect(actions.find((a) => a.id === "detail")).toBeTruthy();
  });

  it("makes retry primary on refuted and failed status", () => {
    expect(ringActionsForNode({ ...taskBound, node_type: "refuted" })[0]).toMatchObject({
      id: "retry",
      primary: true,
    });
    expect(
      ringActionsForNode({ ...taskBound, node_type: "probe", status: "failed" })[0],
    ).toMatchObject({ id: "retry", primary: true });
  });

  it("shows explore for an idle probe and hides recovery actions", () => {
    const actions = ringActionsForNode({ ...base, node_type: "probe" });
    expect(actions[0]).toMatchObject({ id: "fork", group: "explore" });
    expect(actions.some((a) => a.id === "retry")).toBe(false);
    expect(actions.some((a) => a.id === "reassign")).toBe(false);
  });

  it("hides task-only recovery actions when the node has no task anchor", () => {
    const actions = ringActionsForNode({ ...base, node_type: "probe", status: "failed" });
    expect(actions.some((action) => action.id === "retry")).toBe(false);
    expect(actions.some((action) => action.id === "reassign")).toBe(false);
  });

  it("marks system node types that skip the ring", () => {
    expect(SYSTEM_NODE_TYPES.has("stage_gate")).toBe(true);
    expect(SYSTEM_NODE_TYPES.has("dead_end")).toBe(false);
  });
});

describe("ResearchNodeActionRing", () => {
  it("renders 2×3 ring and fires primary retry for dead_end", () => {
    const onAction = vi.fn();
    render(
      <ResearchNodeActionRing
        node={{ ...taskBound, node_type: "dead_end" }}
        mode="ring"
        onAction={onAction}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByRole("menu", { name: "Node actions" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: "Retry" }));
    expect(onAction).toHaveBeenCalledWith("retry");
  });

  it("narrow sheet lists actions with Esc close", () => {
    const onClose = vi.fn();
    render(
      <ResearchNodeActionRing
        node={{ ...base, node_type: "finding" }}
        mode="sheet"
        onAction={vi.fn()}
        onClose={onClose}
      />,
    );
    const dialog = screen.getByRole("dialog", { name: "Node actions" });
    expect(dialog).toBeInTheDocument();
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(onClose).toHaveBeenCalled();
  });

  it("hides unavailable actions instead of leaking disabled engineering reasons", () => {
    render(
      <ResearchNodeActionRing
        node={{ ...taskBound, node_type: "probe", status: "active" }}
        mode="ring"
        onAction={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    expect(screen.queryByRole("menuitem", { name: "Retry" })).toBeNull();
    expect(screen.queryByText(/API is not available yet|Retry is only/)).toBeNull();
    expect(screen.getByRole("menuitem", { name: "Reassign" }).tabIndex).toBe(0);
  });

  it("arrows rove focus across ring items", () => {
    render(
      <ResearchNodeActionRing
        node={{ ...taskBound, node_type: "dead_end" }}
        mode="ring"
        onAction={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    const menu = screen.getByRole("menu", { name: "Node actions" });
    const retry = screen.getByRole("menuitem", { name: "Retry" });
    expect(retry.tabIndex).toBe(0);
    fireEvent.keyDown(menu, { key: "End" });
    const copy = screen.getByRole("menuitem", { name: "Copy" });
    expect(copy.tabIndex).toBe(0);
    expect(retry.tabIndex).toBe(-1);
  });
});
