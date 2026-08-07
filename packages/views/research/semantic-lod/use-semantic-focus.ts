"use client";

/**
 * Semantic LOD — promotion & focus orchestration hook (LRM-1488).
 *
 * Drives the click-to-promote interaction (route-topology §7, viewport §1):
 *   select → open Inspector → compute safe-centre camera → move.
 *
 * Orchestration invariants:
 *   - Continuous clicks: each `promoteAndFocus` supersedes the last camera
 *     intent (the canonical controller re-baselines from the current viewport),
 *     and Back always restores the LOD, selection and viewport captured at the
 *     start of the interaction.
 *   - Reduced Motion: the controller snaps straight to the safe centre
 *     (duration 0) with an aria-live announcement — no probe/no drift and the
 *     classification still lands.
 *   - User pan/wheel/pinch cancels the auto camera via `userInteracted()`.
 */
import { useCallback, useRef } from "react";
import type { SemanticCameraHandle } from "./camera";
import type { NodeBounds, Viewport } from "./camera";

export interface UseSemanticFocusArgs {
  /** Existing semantic camera handle (bound to the 260ms controller). */
  camera: SemanticCameraHandle;
  getViewport: () => Viewport;
  applyViewport: (viewport: Viewport) => void;
  /** Write the selected node id (spec step 1). */
  setSelectedId: (id: string | null) => void;
  /** Open the Inspector for a node (spec step 2) — returns overlay insets. */
  openInspector: (nodeId: string) => void;
  /** Reclassify the graph with `id` promoted (selected → Landmark). */
  promoteLod: (id: string) => void;
  /** Restore the original LOD classification (Back). */
  restoreLod: () => void;
  /** Polite live-region channel for reduced-motion snap announcements. */
  announce?: (text: string) => void;
}

export interface SemanticFocusHandlers {
  /** Select, promote, open Inspector and settle the camera at safe centre. */
  promoteAndFocus: (
    nodeId: string,
    bounds: NodeBounds,
    label?: string,
  ) => void;
  /** Restore original LOD, selection and viewport captured at promotion. */
  back: () => void;
  /** Cancel any in-flight auto camera (user interaction). */
  userInteracted: () => void;
  /** True while a focus animation is running or queued. */
  readonly hasPendingFocus: boolean;
  /**
   * Set by the orchestration just before a focus move and cleared on Back —
   * lets a caller bind the 3s camera lockout (viewport §1) reactively.
   */
  readonly focusActive: boolean;
}

export function useSemanticFocus(
  args: UseSemanticFocusArgs,
): SemanticFocusHandlers {
  const argsRef = useRef(args);
  argsRef.current = args;

  // Snapshot of the viewport + selection before the promotion, for Back.
  const restoreSnapshotRef = useRef<{
    viewport: Viewport;
    selectedId: string | null;
  } | null>(null);
  const focusActiveRef = useRef(false);

  const promoteAndFocus = useCallback(
    (nodeId: string, bounds: NodeBounds, label?: string) => {
      const args = argsRef.current;

      // Step 1–2: selection first, then Inspector (so its overlay insets are
      // reflected in the safe-centre computation at apply time).
      args.setSelectedId(nodeId);
      args.openInspector(nodeId);

      // Promote the node to Landmark (reclassified while protected).
      args.promoteLod(nodeId);

      // Capture the pre-promotion viewport + selection for Back.
      restoreSnapshotRef.current = {
        viewport: args.getViewport(),
        selectedId: nodeId,
      };

      // Step 3–6: compute + apply the safe-centre camera. The handle decides
      // whether a move is needed (72px safe-centre radius) and defers to the
      // canonical controller for 260ms ease / supersede / reduced-motion snap.
      const moved = args.camera.focus(bounds, label ?? nodeId);
      focusActiveRef.current = moved;

      if (!moved && args.announce) {
        args.announce("已选中，已在可视安全区");
      }
    },
    [],
  );

  const back = useCallback(() => {
    const snapshot = restoreSnapshotRef.current;
    if (!snapshot) return;
    restoreSnapshotRef.current = null;
    focusActiveRef.current = false;

    const args = argsRef.current;
    // Restore LOD, selection and viewport in that order.
    args.restoreLod();
    args.setSelectedId(snapshot.selectedId);
    args.applyViewport(snapshot.viewport);
  }, []);

  const userInteracted = useCallback(() => {
    argsRef.current.camera.userInteracted();
  }, []);

  return {
    promoteAndFocus,
    back,
    userInteracted,
    get hasPendingFocus() {
      return argsRef.current.camera.hasPendingFocus;
    },
    get focusActive() {
      return focusActiveRef.current;
    },
  };
}
