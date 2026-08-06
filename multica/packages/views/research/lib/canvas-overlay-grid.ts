/**
 * LRM-797 — canvas overlay grid spacing.
 * Desktop: bottom-left Controls + detail card 12px above;
 *          bottom-right MiniMap + FAB 12px above (no overlap).
 */

export const OVERLAY_GAP_PX = 12;
export const OVERLAY_INSET_PX = 16;

/** Controls / dock height (toolbar pill; matches ResearchCanvasDock). */
export const CONTROLS_HEIGHT_PX = 52;

/** MiniMap box used for FAB stacking (matches MiniMap class sizing). */
export const MINIMAP_HEIGHT_PX = 120;
export const MINIMAP_WIDTH_PX = 168;

export const FAB_SIZE_PX = 48;

/** Bottom offset for Controls (left stack). */
export const CONTROLS_BOTTOM_PX = OVERLAY_INSET_PX;

/** Bottom offset for detail card = controls + gap. */
export const DETAIL_CARD_BOTTOM_PX =
  CONTROLS_BOTTOM_PX + CONTROLS_HEIGHT_PX + OVERLAY_GAP_PX;

/** Bottom offset for MiniMap (right stack). */
export const MINIMAP_BOTTOM_PX = OVERLAY_INSET_PX;

/** Bottom offset for FAB above MiniMap. */
export const FAB_ABOVE_MINIMAP_BOTTOM_PX =
  MINIMAP_BOTTOM_PX + MINIMAP_HEIGHT_PX + OVERLAY_GAP_PX;

/** Narrow: FAB alone at inset (no MiniMap). */
export const FAB_NARROW_BOTTOM_PX = OVERLAY_INSET_PX;
