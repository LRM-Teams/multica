/**
 * FE-05 — pure camera geometry for the research canvas.
 *
 * Everything here is a deterministic function of (world bounds, viewport size,
 * zoom, overlay insets). It has no React or DOM dependency so it can be unit
 * tested in the node environment and reused by any render layer (React Flow
 * today, the FE-04 ViewModel render layer next).
 *
 * The "safe center region" is the area of the viewport that is not covered by
 * the canvas overlays (bottom dock/Controls, top-left panel, bottom-right
 * MiniMap, bottom-left detail card). A focused node is moved to the centre of
 * that region — so it is never hidden behind an overlay — while the camera
 * zoom is preserved.
 *
 * Viewport convention matches React Flow: `{ x, y, zoom }` where `(x, y)` is
 * the world-space coordinate at the top-left of the visible area.
 */

export interface Point {
  x: number;
  y: number;
}

export interface Size {
  width: number;
  height: number;
}

export interface Viewport {
  x: number;
  y: number;
  zoom: number;
}

/** World-space node bounding box (position + measured size). */
export interface NodeBounds {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** Overlay insets in screen (viewport) pixels. */
export interface Insets {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** The screen-space rectangle not covered by canvas overlays. */
export function safeViewportRegion(viewportSize: Size, insets: Insets): Rect {
  const x = Math.max(0, insets.left);
  const y = Math.max(0, insets.top);
  const right = Math.max(0, insets.right);
  const bottom = Math.max(0, insets.bottom);
  const width = Math.max(1, viewportSize.width - x - right);
  const height = Math.max(1, viewportSize.height - y - bottom);
  return { x, y, width, height };
}

/** Centre of the safe region in screen pixels. */
export function safeCenterPoint(viewportSize: Size, insets: Insets): Point {
  const region = safeViewportRegion(viewportSize, insets);
  return {
    x: region.x + region.width / 2,
    y: region.y + region.height / 2,
  };
}

/** Centre of a world-space node bounds. */
export function boundsCenter(bounds: NodeBounds): Point {
  return {
    x: bounds.x + bounds.width / 2,
    y: bounds.y + bounds.height / 2,
  };
}

/**
 * The viewport translation (preserving `zoom`) that places the centre of
 * `bounds` at the centre of the safe region. If the node is already fully
 * inside the safe region and close to it, we still snap to the safe-centre for
 * deterministic behaviour (callers pass `snapThreshold` to skip trivial moves).
 */
export function viewportCenterOnBounds(
  bounds: NodeBounds,
  zoom: number,
  viewportSize: Size,
  insets: Insets,
): Viewport {
  const safeCenter = safeCenterPoint(viewportSize, insets);
  const center = boundsCenter(bounds);
  return {
    x: center.x - safeCenter.x / zoom,
    y: center.y - safeCenter.y / zoom,
    zoom,
  };
}

/**
 * World-space rectangle currently visible at a given viewport. Used to decide
 * whether a focused node is already comfortably on-screen (so we can skip a
 * jarring recentre when the user just pressed focus on a visible node).
 */
export function visibleWorldRegion(
  viewport: Viewport,
  viewportSize: Size,
): Rect {
  return {
    x: viewport.x,
    y: viewport.y,
    width: viewportSize.width / viewport.zoom,
    height: viewportSize.height / viewport.zoom,
  };
}

/**
 * True when `bounds` is fully within `region`, expanded by `padding` (in world
 * units).
 */
export function isWithinRegion(
  bounds: NodeBounds,
  region: Rect,
  padding: number,
): boolean {
  return (
    bounds.x >= region.x + padding &&
    bounds.y >= region.y + padding &&
    bounds.x + bounds.width <= region.x + region.width - padding &&
    bounds.y + bounds.height <= region.y + region.height - padding
  );
}
