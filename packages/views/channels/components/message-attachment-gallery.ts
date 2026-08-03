/**
 * LRM-1098 / LRM-813 scheme A — multi-image gallery layout helpers.
 *
 * Equal-cell grid + cover by default; when the widest/tallest aspect ratio
 * among measured images differs by more than ASPECT_STACK_RATIO, stack in a
 * single column so a short landscape isn't glued beside a giant portrait.
 */

/** Stack when max(aspect) / min(aspect) exceeds this (spec: 比例差 > 2). */
export const ASPECT_STACK_RATIO = 2;

/**
 * Decide stack vs side-by-side grid from natural width/height aspect ratios
 * (`width / height`). Needs at least two positive ratios.
 */
export function shouldStackByAspectRatios(aspectRatios: number[]): boolean {
  const ratios = aspectRatios.filter((r) => Number.isFinite(r) && r > 0);
  if (ratios.length < 2) return false;
  const min = Math.min(...ratios);
  const max = Math.max(...ratios);
  return max / min > ASPECT_STACK_RATIO;
}

export type GalleryLayoutMode = "grid" | "stack";

/** Prefer stack once enough aspects are known; otherwise keep the equal grid. */
export function resolveGalleryLayout(
  aspectRatios: Array<number | undefined>,
  imageCount: number,
): GalleryLayoutMode {
  if (imageCount < 2) return "grid";
  const known = aspectRatios.filter(
    (r): r is number => typeof r === "number" && Number.isFinite(r) && r > 0,
  );
  if (known.length < 2) return "grid";
  return shouldStackByAspectRatios(known) ? "stack" : "grid";
}
