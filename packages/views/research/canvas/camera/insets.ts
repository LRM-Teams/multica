/**
 * FE-05 — map the canvas overlay grid into camera safe-region insets.
 *
 * Reuses the canonical LRM-797 / LRM-1151 overlay measurements so the camera
 * never parks a focused node underneath the dock, MiniMap, top-left module
 * label, or the bottom-left detail card.
 */

import type { Insets } from "./geometry";

/**
 * Standard desktop canvas insets. The dock/Controls sits bottom-centre, the
 * MiniMap bottom-right, and the detail overlay bottom-left — so the bottom
 * reserved band is the height of the tallest bottom stack, the right band the
 * MiniMap width, and the top band the top-left module panel.
 */
export const CANVAS_OVERLAY_INSETS: Insets = {
  top: 56,
  right: 168 + 16,
  bottom: 84,
  left: 16,
};

/** Narrow (mobile) canvas — only a bottom FAB/dock, no MiniMap or side panel. */
export const CANVAS_OVERLAY_INSETS_NARROW: Insets = {
  top: 56,
  right: 16,
  bottom: 68,
  left: 16,
};
