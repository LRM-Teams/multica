import { fireEvent, screen, waitFor } from "@testing-library/react";
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

  it("uses roving focus with arrows and Home/End across trajectory commits", () => {
    const nodes = [
      node("a", "Question", "done", "theme-main"),
      node("b", "Branch task", "active", "theme-b"),
      node("c", "Result", "done", "theme-b"),
    ];
    const onSelect = vi.fn();
    renderWithI18n(
      <TrajectoryExplorer
        nodes={nodes}
        sessionStatus="running"
        onSelect={onSelect}
        onJumpToCanvas={vi.fn()}
        onOpenNodeDetail={vi.fn()}
      />,
    );
    const cards = screen.getAllByTestId("trajectory-commit-card");
    expect(cards.map((card) => card.tabIndex)).toEqual([0, -1, -1]);

    cards[0]!.focus();
    fireEvent.keyDown(cards[0]!, { key: "ArrowDown" });
    expect(onSelect).toHaveBeenLastCalledWith("b");
    fireEvent.keyDown(cards[1]!, { key: "End" });
    expect(onSelect).toHaveBeenLastCalledWith("c");
    fireEvent.keyDown(cards[2]!, { key: "Home" });
    expect(onSelect).toHaveBeenLastCalledWith("a");
  });

  it("localizes graph, overview, and status semantics", () => {
    const nodes = [node("a", "问题", "done", "theme-main")];
    renderWithI18n(
      <TrajectoryExplorer
        nodes={nodes}
        sessionStatus="running"
        onSelect={vi.fn()}
        onJumpToCanvas={vi.fn()}
        onOpenNodeDetail={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );

    expect(screen.getByTestId("trajectory-explorer")).toHaveAttribute(
      "aria-label",
      "轨迹探索器",
    );
    expect(screen.getByTestId("trajectory-graph")).toHaveAttribute(
      "aria-label",
      "探索轨迹图",
    );
    expect(screen.getByTestId("trajectory-minimap").querySelector("svg")).toHaveAttribute(
      "aria-label",
      "轨迹概览",
    );
    expect(screen.getByTestId("trajectory-commit-status")).toHaveTextContent("完成");
  });

  it("labels canonical succeeded results as completed and idle Agents as idle", () => {
    const completed = node("result", "已核验结果", "succeeded", "theme-main");
    const idleAgent: ResearchGraphNode = {
      ...node("agent", "市场研究员", "idle", "theme-agent", "agent-market"),
      node_type: "agent",
      created_at: "2026-01-02T00:00:00Z",
    };
    renderWithI18n(
      <TrajectoryExplorer
        nodes={[completed, idleAgent]}
        selectedId="agent"
        sessionStatus="running"
        onSelect={vi.fn()}
        onJumpToCanvas={vi.fn()}
        onOpenNodeDetail={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );

    expect(
      screen
        .getAllByTestId("trajectory-commit-status")
        .map((status) => status.textContent),
    ).toEqual(["完成", "空闲"]);
    expect(screen.getByTestId("trajectory-detail")).toHaveTextContent("空闲");
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

  it("closes inline detail on Escape and restores focus to the selected commit", async () => {
    const nodes = [node("a", "Question", "done", "theme-main")];
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

    const commit = screen.getByTestId("trajectory-commit-card");
    await user.click(commit);
    const explorer = screen.getByTestId("trajectory-explorer");
    fireEvent.keyDown(explorer, { key: "Escape" });

    expect(onSelect).toHaveBeenLastCalledWith(null);
    expect(screen.getByTestId("trajectory-detail-empty")).toBeInTheDocument();
    await waitFor(() => expect(document.activeElement).toBe(commit));
  });

  it("localizes inline detail status", () => {
    renderWithI18n(
      <TrajectoryExplorer
        nodes={[node("a", "问题", "abandoned", "theme-main")]}
        selectedId="a"
        sessionStatus="running"
        onSelect={vi.fn()}
        onJumpToCanvas={vi.fn()}
        onOpenNodeDetail={vi.fn()}
      />,
      { locale: "zh-Hans" },
    );

    expect(screen.getByTestId("trajectory-detail")).toHaveTextContent("已放弃");
  });
});
