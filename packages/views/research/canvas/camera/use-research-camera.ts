/**
 * FE-05 — React Flow adapter for the research camera controller.
 *
 * Binds the pure `ResearchCameraController` to a live React Flow instance so
 * the V5 canvas (and, later, the V6 render layer) gets safe-centre focusing,
 * interruption-safe animation, drag hand-off, reduced motion and a11y
 * announcement without duplicating that logic in the component.
 *
 * This adapter stays inside the views package (no `next/*`, no router) and
 * only talks to the abstract `ResearchCameraDriver` contract.
 */

import { useCallback, useMemo, useRef } from "react";
import { CANVAS_OVERLAY_INSETS } from "./insets";
import { ResearchCameraController } from "./controller";
import type { NodeBounds } from "./geometry";

/** Structural viewport shape compatible with React Flow's {x,y,zoom}. */
export type CameraViewportShape = { x: number; y: number; zoom: number };

export interface ResearchCameraHandlers {
  /**
   * Move the camera to put the node (by measured bounds) into the safe centre
   * region. Returns true when a move is issued (or the target is already
   * centred).
   */
  focus: (bounds: NodeBounds, label?: string) => boolean;
  /** Call on any user pan/drag so the controller releases the camera. */
  userInteracted: () => void;
  /** Cancel any in-flight auto-move. */
  cancel: () => void;
  /** True while an auto-move is running or queued. */
  readonly hasPendingFocus: boolean;
}

export interface UseResearchCameraArgs {
  getViewport: () => CameraViewportShape;
  setViewport: (viewport: CameraViewportShape) => void;
  getContainerSize: () => { width: number; height: number };
  reducedMotion: boolean;
  announce?: (text: string) => void;
}

/**
 * Create a renderer-facing camera handle bound to React Flow's viewport
 * primitives. The controller is created once and kept stable; the driver
 * closures read the latest container/viewport through the caller's hooks.
 */
export function useResearchCamera(
  args: UseResearchCameraArgs,
): ResearchCameraHandlers {
  const announceRef = useRef(args.announce);
  announceRef.current = args.announce;
  const reducedMotionRef = useRef(args.reducedMotion);
  reducedMotionRef.current = args.reducedMotion;

  const controller = useMemo(
    () =>
      new ResearchCameraController({
        viewportSize: () => args.getContainerSize(),
        getViewport: () => args.getViewport(),
        applyViewport: (vp) =>
          args.setViewport({ x: vp.x, y: vp.y, zoom: vp.zoom }),
        insets: () => CANVAS_OVERLAY_INSETS,
        reducedMotion: () => reducedMotionRef.current,
        announce: (text) => announceRef.current?.(text),
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const focus = useCallback(
    (bounds: NodeBounds, label?: string): boolean => {
      if (bounds.width <= 0 || bounds.height <= 0) return false;
      controller.focus({ bounds, label });
      return true;
    },
    [controller],
  );

  const userInteracted = useCallback(() => {
    controller.userInteracted();
  }, [controller]);

  const cancel = useCallback(() => {
    controller.cancel();
  }, [controller]);

  return useMemo(
    () => ({
      focus,
      userInteracted,
      cancel,
      get hasPendingFocus() {
        return controller.hasPendingFocus;
      },
    }),
    [controller, focus, userInteracted, cancel],
  );
}
