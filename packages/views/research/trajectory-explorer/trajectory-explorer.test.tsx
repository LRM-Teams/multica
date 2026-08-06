import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { TrajectoryExplorer } from "./trajectory-explorer";

function node(
  id: string,
  title: string,
  status = "active",
  theme?: string,
  actor?: string,
): ResearchGraphNode {
  return {
    id,
    session_id: "s1",
    node_type: "task",
    title,
    summary: `${title} summary`,
    status,
    actor_agent_id: actor ?? null,
    payload: {},
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    theme_key: theme ?? "theme-main",
  };
}

describe("TrajectoryExplorer (LRM-1480 / UI-06)", () => {
  it("renders empty state without fake branches", () => {
    renderWithI18n(
      <TrajectoryExplorer
        nodes={[]}
        sessionStatus="running"
        onSelect={vi.fn()}
        onJumpToCanvas={vi.fn()}
        onOpenNodeDetail={vi.fn()}
      />,
    );
    expect(screen.getByTestId("trajectory-empty")).toBeInTheDocument();
  });

  it("renders a virtualized graph for a small fixture and selects a card", async () => {
    const nodes = [
      node("a", "Question", "done", "theme-main"),
      node("b", "Branch task", "active", "theme-b"),
      node("c", "Result", "done", "theme-b"),
    ];
    const onSelect = vi.fn();
    const user = userEvent.setup();
    renderWithI18n(
      <TrajectoryExplorer
        nodes={nodes}
        sessionStatus="running"
        onSelect={onSelect}
        onJumpToCanvas={vi.fn()}
        onOpenNodeDetail={vi.fn()}
      />,
    );
    const graph = screen.getByTestId("trajectory-graph");
    expect(graph).toBeInTheDocument();
    const cards = screen.getAllByTestId("trajectory-commit-card");
    expect(cards.length).toBe(3);
    await user.click(cards[1]!);
    expect(onSelect).toHaveBeenCalledWith("b");
  });

  it("shows loading skeleton without fake commits", () => {
    renderWithI18n(
      <TrajectoryExplorer
        nodes={[]}
        sessionStatus="running"
        loading
        onSelect={vi.fn()}
        onJumpToCanvas={vi.fn()}
        onOpenNodeDetail={vi.fn()}
      />,
    );
    expect(screen.getByTestId("trajectory-loading")).toBeInTheDocument();
    expect(screen.queryByTestId("trajectory-commit-card")).not.toBeInTheDocument();
  });

  it("jump to canvas selects the node and invokes the callback", async () => {
    const nodes = [node("a", "Question", "done", "theme-main")];
    const onJump = vi.fn();
    const onSelect = vi.fn();
    const user = userEvent.setup();
    renderWithI18n(
      <TrajectoryExplorer
        nodes={nodes}
        sessionStatus="running"
        selectedId="a"
        onSelect={onSelect}
        onJumpToCanvas={onJump}
        onOpenNodeDetail={vi.fn()}
      />,
    );
    const detail = screen.getByTestId("trajectory-detail");
    expect(detail).toBeInTheDocument();
    await user.click(screen.getByTestId("trajectory-detail-jump"));
    expect(onJump).toHaveBeenCalledWith("a");
  });
});
