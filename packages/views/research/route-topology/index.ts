/**
 * Organic route topology & geometry engine — public model (LRM-1487 / 实现-11).
 *
 * This barrel exposes ONLY the pure, worker-ready interfaces the renderer may
 * consume. It never imports a React/DOM module; it never authors canonical
 * graph facts. Consumers read `RouteLayout` and never mutate `RouteTopology`.
 */
export * from "./types";
export * from "./seed";
export * from "./outcome";
export * from "./geometry";
export * from "./topology";
export * from "./layout";
