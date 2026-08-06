/**
 * FE-05 — research canvas camera/focus layer.
 *
 * Moves a focused node to the safe centre of the canvas, with interruption-safe
 * animation (rapid clicks never stack or drift), immediate hand-off on user
 * drag, reduced-motion support, and keyboard/screen-reader focus feedback.
 *
 * Built against the FE-04 unified ViewModel geometry (node position from the
 * render layer), renderer-agnostic so it works with the React Flow V5 layer
 * today and the V6 render layer next.
 */
export * from "./geometry";
export * from "./animator";
export * from "./controller";
export * from "./insets";
export * from "./use-research-camera";
