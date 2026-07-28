/**
 * Pure helpers for the circular avatar crop (LRM-542).
 *
 * Kept out of the `AvatarCropDialog` component file so the component file only
 * exports components — otherwise react-doctor's `only-export-components` rule
 * fires and Fast Refresh can't safely preserve state.
 */

/** Output avatar resolution (LRM-542 SoT §2: square 512² PNG output). */
export const AVATAR_OUTPUT_SIZE = 512;

/**
 * Source-rect computation for the circular avatar crop.
 *
 * Model: the image is shown with a "cover" base fit into a square stage (the
 * smaller natural dimension == stage edge), then scaled by `zoom` and panned by
 * `panX`/`panY` (stage CSS px). The visible square maps to this source rect,
 * which the caller draws into a 512² canvas.
 *
 * `panX > 0` means the image was dragged right, so the visible window shifts
 * left (toward smaller source x).
 */
export function computeAvatarCropSourceRect(params: {
  naturalWidth: number;
  naturalHeight: number;
  stageSize: number;
  zoom: number;
  panX: number;
  panY: number;
}): { sx: number; sy: number; sw: number; sh: number } {
  const { naturalWidth: nw, naturalHeight: nh, stageSize, zoom, panX, panY } = params;
  const minDim = Math.min(nw, nh);
  const coverScale = stageSize / minDim;
  // Source side visible inside the square stage at this zoom.
  const sw = minDim / zoom;
  const sh = sw;
  // Stage-px → source-px at the current zoom (display scale = coverScale * zoom).
  const pxToSrc = 1 / (coverScale * zoom);
  const centerX = nw / 2 - panX * pxToSrc;
  const centerY = nh / 2 - panY * pxToSrc;
  const sx = Math.max(0, Math.min(centerX - sw / 2, nw - sw));
  const sy = Math.max(0, Math.min(centerY - sh / 2, nh - sh));
  return { sx, sy, sw, sh };
}
