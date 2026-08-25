import { describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import type { DragEndEvent, DragOverEvent, DragStartEvent } from "@dnd-kit/core";
import type { Issue } from "@multica/core/types";
import type { BoardColumnGroup } from "../components/board-column";
import { useIssueGroupDrag } from "./use-issue-group-drag";

function issue(id: string, status: Issue["status"], position: number): Issue {
  return { id, status, position } as Issue;
}

const groups: BoardColumnGroup[] = [
  { id: "status:todo", title: "todo", status: "todo", createData: { status: "todo" } },
  {
    id: "status:in_progress",
    title: "in_progress",
    status: "in_progress",
    createData: { status: "in_progress" },
  },
];

function dragStart(id: string): DragStartEvent {
  return { active: { id } } as DragStartEvent;
}

function dragOver(activeId: string, overId: string): DragOverEvent {
  return { active: { id: activeId }, over: { id: overId } } as DragOverEvent;
}

function dragEnd(activeId: string, overId: string): DragEndEvent {
  return { active: { id: activeId }, over: { id: overId } } as DragEndEvent;
}

describe("useIssueGroupDrag", () => {
  it("owns the shared manual-sort drag state and move decision", () => {
    const issues = [issue("a", "todo", 1), issue("b", "in_progress", 10)];
    const onMoveIssue = vi.fn();
    const { result } = renderHook(() =>
      useIssueGroupDrag({
        issues,
        groups,
        grouping: "status",
        sortBy: "position",
        onMoveIssue,
      }),
    );

    expect(result.current.columns).toEqual({
      "status:todo": ["a"],
      "status:in_progress": ["b"],
    });

    act(() => result.current.dndContextProps.onDragStart(dragStart("a")));
    expect(result.current.activeIssue?.id).toBe("a");

    act(() => result.current.dndContextProps.onDragOver(dragOver("a", "b")));
    expect(result.current.columns).toEqual({
      "status:todo": [],
      "status:in_progress": ["a", "b"],
    });

    act(() => result.current.dndContextProps.onDragEnd(dragEnd("a", "b")));
    expect(onMoveIssue).toHaveBeenCalledWith(
      "a",
      { status: "in_progress", position: 11 },
      expect.any(Function),
    );
    expect(result.current.activeIssue).toBeNull();
  });

  it("keeps sorted columns stable and preserves position on a cross-group move", () => {
    const issues = [issue("a", "todo", 3), issue("b", "in_progress", 10)];
    const onMoveIssue = vi.fn();
    const { result } = renderHook(() =>
      useIssueGroupDrag({
        issues,
        groups,
        grouping: "status",
        sortBy: "priority",
        onMoveIssue,
      }),
    );

    act(() => result.current.dndContextProps.onDragOver(dragOver("a", "b")));
    expect(result.current.columns["status:todo"]).toEqual(["a"]);

    act(() => result.current.dndContextProps.onDragEnd(dragEnd("a", "b")));
    expect(onMoveIssue).toHaveBeenCalledWith(
      "a",
      { status: "in_progress", position: 3 },
      expect.any(Function),
    );
  });
});
