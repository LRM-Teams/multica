/** Survives Virtuoso / list remounts for FAB chat bodies (LRM-987). */
const expandedByContentKey = new Map<string, true>();

export function isCollapsibleMessageExpanded(contentKey: string): boolean {
  return expandedByContentKey.has(contentKey);
}

export function setCollapsibleMessageExpanded(contentKey: string, expanded: boolean): void {
  if (expanded) expandedByContentKey.set(contentKey, true);
  else expandedByContentKey.delete(contentKey);
}

/** Test-only: clear remount-survival cache. */
export function resetCollapsibleMessageExpandedMemoryForTests(): void {
  expandedByContentKey.clear();
}
