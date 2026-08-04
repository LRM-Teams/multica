import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { TrajectoryCommit } from "@multica/core/research";
import { TrajectoryCommitCard } from "./trajectory-commit-card";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      values?: Record<string, unknown>,
    ) => {
      const value = fn({
        trajectory: {
          status: {
            running: "Active",
            success: "Concluded",
            detour: "Detour",
            failed: "Failed",
            merged: "Merged",
          },
          by_agent: "By {{agent}}",
          evidence_count: "{{count}} evidence",
          aggregate_count: "{{count}} commits",
          details: "Commit details",
          branch: "Branch {{branch}}",
          parents: "{{count}} parents",
          failure_reason: "Failure reason",
          view_node: "View research node",
          view_evidence: "View sources and evidence",
          filter_branch: "Only this branch",
          copy_link: "Copy link",
          no_node_ref: "No linked research node",
          no_evidence_ref: "No linked source or evidence",
        },
      }) as string;
      return Object.entries(values ?? {}).reduce(
        (text, [key, value]) => text.replace(`{{${key}}}`, String(value)),
        value,
      );
    },
  }),
}));

const commit: TrajectoryCommit = {
  id: "commit-1",
  parentIds: ["parent-a", "parent-b"],
  unknownParentIds: [],
  parentRefs: [],
  branchId: "evidence-check",
  agentId: "Agent Atlas",
  timestamp: "2026-08-04T10:02:00Z",
  title: "验证跨语言来源并形成结论 with a deliberately long tail",
  summary:
    "A long failure reason that must stay out of the DOM until details open.",
  status: "failed",
  evidenceRefs: ["ev-1"],
  relationshipSource: "explicit",
  sourceNodeIds: ["node-1"],
  taskId: "task-1",
  attempt: 1,
  sequence: 2,
};

describe("TrajectoryCommitCard (LRM-1392)", () => {
  it("makes actor, conclusion, status, time, evidence and merge parents scannable", () => {
    render(<TrajectoryCommitCard commit={commit} />);
    const card = screen.getByRole("button", { name: /Agent Atlas.*Failed/i });
    expect(within(card).getByText(/验证跨语言来源/)).toBeInTheDocument();
    expect(within(card).getByText("2 parents")).toBeInTheDocument();
    expect(within(card).getByText("1 evidence")).toBeInTheDocument();
    expect(card).toHaveAttribute("data-trajectory-status", "failed");
    expect(card.querySelector("[data-status-shape='failed']")).toBeTruthy();
    expect(screen.queryByText(/long failure reason/i)).toBeNull();
  });

  it.each(["Enter", " "])(
    "opens details with %s and Escape closes with focus restored",
    (key) => {
      render(<TrajectoryCommitCard commit={commit} />);
      const card = screen.getByRole("button", { name: /Agent Atlas.*Failed/i });
      card.focus();
      fireEvent.keyDown(card, { key });
      expect(
        screen.getByRole("dialog", { name: commit.title }),
      ).toBeInTheDocument();
      expect(screen.getByText(/long failure reason/i)).toBeInTheDocument();
      fireEvent.keyDown(document, { key: "Escape" });
      expect(screen.queryByRole("dialog", { name: commit.title })).toBeNull();
      expect(card).toHaveFocus();
    },
  );

  it("wires each action independently", () => {
    const callbacks = {
      onViewNode: vi.fn(),
      onViewEvidence: vi.fn(),
      onFilterBranch: vi.fn(),
      onCopyLink: vi.fn(),
    };
    render(<TrajectoryCommitCard commit={commit} {...callbacks} defaultOpen />);
    fireEvent.click(screen.getByRole("button", { name: "View research node" }));
    fireEvent.click(
      screen.getByRole("button", { name: "View sources and evidence" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Only this branch" }));
    fireEvent.click(screen.getByRole("button", { name: "Copy link" }));
    expect(callbacks.onViewNode).toHaveBeenCalledWith(commit);
    expect(callbacks.onViewEvidence).toHaveBeenCalledWith(commit);
    expect(callbacks.onFilterBranch).toHaveBeenCalledWith("evidence-check");
    expect(callbacks.onCopyLink).toHaveBeenCalledWith(commit);
  });

  it("disables unavailable references and exposes the reasons", () => {
    render(
      <TrajectoryCommitCard
        commit={{ ...commit, sourceNodeIds: [], evidenceRefs: [] }}
        defaultOpen
      />,
    );
    expect(
      screen.getByRole("button", { name: "View research node" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "View sources and evidence" }),
    ).toBeDisabled();
    expect(screen.getByText("No linked research node")).toBeInTheDocument();
    expect(
      screen.getByText("No linked source or evidence"),
    ).toBeInTheDocument();
  });

  it("supports compact, selected, aggregate and 99+ evidence surfaces", () => {
    const { rerender } = render(
      <TrajectoryCommitCard
        commit={{
          ...commit,
          evidenceRefs: Array.from({ length: 100 }, (_, i) => `ev-${i}`),
        }}
        size="compact"
        selected
      />,
    );
    expect(screen.getByRole("button")).toHaveAttribute("data-size", "compact");
    expect(screen.getByRole("button")).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("99+")).toBeInTheDocument();
    rerender(
      <TrajectoryCommitCard
        commit={commit}
        size="aggregate"
        aggregateCount={24}
      />,
    );
    expect(screen.getByText("24 commits")).toBeInTheDocument();
    expect(screen.queryByText(commit.title)).toBeNull();
  });
});
