/**
 * Research V6 — node importance → star level (UI-01 / LRM-1475).
 *
 * Pure utility kept out of the component file (react-doctor only-export-components):
 * maps the canonical `importance` value onto a 0..3 star level for the card face.
 */

/** Importance value (0..1 or 1..3) → star level 0..3. */
export function importanceToStars(importance: number | null | undefined): number {
  const n = Number(importance);
  if (Number.isNaN(n)) return 0;
  if (n <= 1) return Math.round(n * 3); // 0..1 → 3-star scale
  return Math.min(3, Math.max(0, Math.round(n)));
}
