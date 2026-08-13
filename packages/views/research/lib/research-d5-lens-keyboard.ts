const NEXT_KEYS = new Set(["ArrowRight", "ArrowDown"]);
const PREVIOUS_KEYS = new Set(["ArrowLeft", "ArrowUp"]);

/** Resolve the ARIA tabs keyboard contract without coupling it to the DOM. */
export function resolveD5LensNavigationIndex(
  key: string,
  currentIndex: number,
  count: number,
): number | null {
  if (count <= 0 || currentIndex < 0 || currentIndex >= count) return null;
  if (key === "Home") return 0;
  if (key === "End") return count - 1;
  if (NEXT_KEYS.has(key)) return (currentIndex + 1) % count;
  if (PREVIOUS_KEYS.has(key)) return (currentIndex - 1 + count) % count;
  return null;
}
