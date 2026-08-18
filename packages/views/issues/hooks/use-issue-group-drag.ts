"use client";

import {
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragOverEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { arrayMove } from "@dnd-kit/sortable";
import type { Issue } from "@multica/core/types";
import type { IssueGrouping, SortField } from "@multica/core/issues/stores/view-store";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { BoardColumnGroup } from "../components/board-column";
import {
  buildColumns,
  computePosition,
  findColumn,
  getMoveUpdates,
  issueMatchesGroup,
  makeKanbanCollision,
  type DragMoveUpdates,
} from "../utils/drag-utils";

type MoveIssue = (
  issueId: string,
  updates: DragMoveUpdates,
  onSettled?: () => void,
) => void;

/**
 * Shared drag state machine for issue views grouped into columns.
 *
 * Board and List own different presentation, but drag synchronization,
 * optimistic column movement, and the final mutation decision are one behavior.
 */
export function useIssueGroupDrag({
  issues,
  groups,
  grouping,
  sortBy,
  onMoveIssue,
}: {
  issues: Issue[];
  groups: BoardColumnGroup[];
  grouping: IssueGrouping;
  sortBy: SortField;
  onMoveIssue?: MoveIssue;
}) {
  const groupIds = useMemo(() => new Set(groups.map((group) => group.id)), [groups]);
  const groupMap = useMemo(
    () => new Map(groups.map((group) => [group.id, group])),
    [groups],
  );
  const collisionDetection = useMemo(() => makeKanbanCollision(groupIds), [groupIds]);

  const [activeIssue, setActiveIssue] = useState<Issue | null>(null);
  const isDraggingRef = useRef(false);
  const isSettlingRef = useRef(false);
  const [settleVersion, setSettleVersion] = useState(0);

  const [columns, setColumns] = useState<Record<string, string[]>>(() =>
    buildColumns(issues, groups, grouping),
  );
  const columnsRef = useRef(columns);
  columnsRef.current = columns;

  useEffect(() => {
    if (!isDraggingRef.current && !isSettlingRef.current) {
      setColumns(buildColumns(issues, groups, grouping));
    }
  }, [issues, groups, grouping, settleVersion]);

  const recentlyMovedRef = useRef(false);
  useEffect(() => {
    const id = requestAnimationFrame(() => {
      recentlyMovedRef.current = false;
    });
    return () => cancelAnimationFrame(id);
  }, [columns]);

  const issueMap = useMemo(() => {
    const map = new Map<string, Issue>();
    for (const issue of issues) map.set(issue.id, issue);
    return map;
  }, [issues]);
  const issueMapRef = useRef(issueMap);
  if (!isDraggingRef.current && !isSettlingRef.current) {
    issueMapRef.current = issueMap;
  }

  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: { distance: 5 },
    }),
  );

  const onDragStart = useCallback((event: DragStartEvent) => {
    isDraggingRef.current = true;
    setActiveIssue(issueMapRef.current.get(event.active.id as string) ?? null);
  }, []);

  const onDragOver = useCallback(
    (event: DragOverEvent) => {
      const { active, over } = event;
      if (!over || recentlyMovedRef.current) return;

      const activeId = active.id as string;
      const overId = over.id as string;
      setColumns((current) => {
        const activeColumn = findColumn(current, activeId, groupIds);
        const overColumn = findColumn(current, overId, groupIds);
        if (!activeColumn || !overColumn || activeColumn === overColumn) return current;
        if (sortBy !== "position") return current;

        recentlyMovedRef.current = true;
        const oldIds = current[activeColumn]!.filter((id) => id !== activeId);
        const newIds = [...current[overColumn]!];
        const overIndex = newIds.indexOf(overId);
        newIds.splice(overIndex >= 0 ? overIndex : newIds.length, 0, activeId);
        return { ...current, [activeColumn]: oldIds, [overColumn]: newIds };
      });
    },
    [groupIds, sortBy],
  );

  const onDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      isDraggingRef.current = false;
      setActiveIssue(null);

      const resetColumns = () => setColumns(buildColumns(issues, groups, grouping));
      if (!over || !onMoveIssue) {
        resetColumns();
        return;
      }

      const activeId = active.id as string;
      const overId = over.id as string;
      const currentColumns = columnsRef.current;
      const activeColumn = findColumn(currentColumns, activeId, groupIds);
      const overColumn = findColumn(currentColumns, overId, groupIds);
      if (!activeColumn || !overColumn) {
        resetColumns();
        return;
      }

      let finalColumns = currentColumns;
      if (activeColumn === overColumn && sortBy === "position") {
        const ids = currentColumns[activeColumn]!;
        const oldIndex = ids.indexOf(activeId);
        const newIndex = ids.indexOf(overId);
        if (oldIndex !== -1 && newIndex !== -1 && oldIndex !== newIndex) {
          const reordered = arrayMove(ids, oldIndex, newIndex);
          finalColumns = { ...currentColumns, [activeColumn]: reordered };
          setColumns(finalColumns);
        }
      }

      const finalColumn =
        sortBy === "position"
          ? findColumn(finalColumns, activeId, groupIds)
          : overColumn;
      const finalGroup = finalColumn ? groupMap.get(finalColumn) : undefined;
      if (!finalColumn || !finalGroup) {
        resetColumns();
        return;
      }

      const map = issueMapRef.current;
      const currentIssue = map.get(activeId);
      if (sortBy !== "position") {
        if (!currentIssue || issueMatchesGroup(currentIssue, finalGroup)) {
          resetColumns();
          return;
        }
        isSettlingRef.current = true;
        onMoveIssue(activeId, getMoveUpdates(finalGroup, currentIssue.position), () => {
          isSettlingRef.current = false;
          setSettleVersion((version) => version + 1);
        });
        return;
      }

      const newPosition = computePosition(finalColumns[finalColumn]!, activeId, map);
      if (
        currentIssue &&
        issueMatchesGroup(currentIssue, finalGroup) &&
        currentIssue.position === newPosition
      ) {
        return;
      }

      isSettlingRef.current = true;
      onMoveIssue(activeId, getMoveUpdates(finalGroup, newPosition), () => {
        isSettlingRef.current = false;
      });
    },
    [groupIds, groupMap, grouping, groups, issues, onMoveIssue, sortBy],
  );

  return {
    activeIssue,
    columns,
    issueMap: issueMapRef.current,
    isDraggingRef,
    dndContextProps: {
      sensors,
      collisionDetection,
      onDragStart,
      onDragOver,
      onDragEnd,
    },
  };
}
