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

describe("ringActionsForNode (LRM-848 / LRM-981)", () => {
  it("makes retry primary on dead_end", () => {
    const actions = ringActionsForNode({ ...base, node_type: "dead_end" });
    expect(actions[0]).toMatchObject({ id: "retry", primary: true });
    expect(actions.find((a) => a.id === "detail")).toBeTruthy();
  });

  it("makes retry primary on refuted and failed status", () => {
    expect(ringActionsForNode({ ...base, node_type: "refuted" })[0]).toMatchObject({
      id: "retry",
      primary: true,
    });
    expect(
      ringActionsForNode({ ...base, node_type: "probe", status: "failed" })[0],
    ).toMatchObject({ id: "retry", primary: true });
  });

  it("makes detail primary on probe and disables retry", () => {
    const actions = ringActionsForNode({ ...base, node_type: "probe" });
    expect(actions[0]).toMatchObject({ id: "detail", primary: true });
    expect(actions.find((a) => a.id === "retry")).toMatchObject({ disabled: true });
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
        node={{ ...base, node_type: "dead_end" }}
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
});
