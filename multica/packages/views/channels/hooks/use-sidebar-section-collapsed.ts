"use client";

import { useCallback, useEffect, useState } from "react";

export type MessagesSidebarSection = "pinned" | "dms" | "agent-dms" | "channels";

/**
 * In-memory cache so Messages sidebar section collapse survives remounts of
 * ChannelsPage / DmList within the same SPA session (LRM-655 — selecting a
 * channel must not re-expand a collapsed DIRECT MESSAGES section).
 */
const collapsedMemory = new Map<string, boolean>();

function storageKey(workspaceId: string, section: MessagesSidebarSection): string {
  return `multica:messages-sidebar:${workspaceId}:${section}:collapsed`;
}

function memoryKey(workspaceId: string, section: MessagesSidebarSection): string {
  return `${workspaceId}:${section}`;
}

function readSession(key: string): boolean | null {
  try {
    const raw = window.sessionStorage.getItem(key);
    if (raw === "1") return true;
    if (raw === "0") return false;
  } catch {
    // private mode / quota — ignore
  }
  return null;
}

function writeSession(key: string, collapsed: boolean): void {
  try {
    window.sessionStorage.setItem(key, collapsed ? "1" : "0");
  } catch {
    // ignore
  }
}

/** Test-only: clear the remount-survival cache. */
export function resetSidebarSectionCollapsedMemoryForTests(): void {
  collapsedMemory.clear();
}

/**
 * Collapse state for Messages sidebar sections (PINNED / DMs / CHANNELS).
 *
 * Defaults to expanded (`false`). Remembers the last toggle in a module Map
 * (survives component remount) and mirrors to `sessionStorage` (survives
 * soft refresh within the tab).
 */
export function useSidebarSectionCollapsed(
  section: MessagesSidebarSection,
  workspaceId: string | null | undefined,
  defaultCollapsed = false,
): [boolean, (next: boolean | ((prev: boolean) => boolean)) => void] {
  const memKey = workspaceId ? memoryKey(workspaceId, section) : null;
  const sessKey = workspaceId ? storageKey(workspaceId, section) : null;

  const [collapsed, setCollapsedState] = useState(() => {
    if (memKey && collapsedMemory.has(memKey)) {
      return collapsedMemory.get(memKey)!;
    }
    return defaultCollapsed;
  });

  // After mount (and when workspace changes), adopt sessionStorage if memory
  // has not already recorded a value for this key.
  useEffect(() => {
    if (!memKey || !sessKey) return;
    if (collapsedMemory.has(memKey)) {
      setCollapsedState(collapsedMemory.get(memKey)!);
      return;
    }
    const fromSession = readSession(sessKey);
    if (fromSession === null) return;
    collapsedMemory.set(memKey, fromSession);
    setCollapsedState(fromSession);
  }, [memKey, sessKey]);

  const setCollapsed = useCallback(
    (next: boolean | ((prev: boolean) => boolean)) => {
      setCollapsedState((prev) => {
        const value = typeof next === "function" ? next(prev) : next;
        if (memKey) collapsedMemory.set(memKey, value);
        if (sessKey) writeSession(sessKey, value);
        return value;
      });
    },
    [memKey, sessKey],
  );

  return [collapsed, setCollapsed];
}
