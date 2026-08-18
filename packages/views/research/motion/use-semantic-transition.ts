/**
 * LRM-1477 — useSemanticTransition React hook.
 *
 * Spec: §6.1 (hook — consumes the queue, maps to DOM class/inline style, owns
 * interrupt / background-restore / reduced-motion / low-perf), §4.
 *
 * Responsibilities (per spec):
 *  - enqueue contract projection deltas into the transition queue reducer;
 *  - drive a RAF clock that advances/settles the queue (throttled to ~30fps
 *    under low-performance, see MOTION_LOW_PERF_FRAME_MS);
 *  - honour `prefers-reduced-motion` (immediate settlement) and a
 *    low-performance profile (no glow, halved budget) — Rule ④/⑤;
 *  - track `document.visibilitychange` so hidden-period events do not replay
 *    and restore collapses the backlog (Rule ⑥);
 *  - expose per-entity directives (class + inline style + persistent static
 *    marker) so a consumer shell can render the animation without us touching
 *    layout / canonical state.
 */

"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import {
  createTransitionQueue,
  transitionQueueReducer,
  liveQueueSize,
  liveTransitionForEntity,
  settledMarkers,
  type MotionProfile,
  type ProjectionTransitionEvent,
  type TransitionQueue,
  type QueuedEntry,
} from "./transition-queue";
import {
  buildMotionDirective,
  type MotionDirective,
  type MotionDirectiveOptions,
} from "./directives";
import { MOTION_LOW_PERF_FRAME_MS } from "./tokens";
import { usePrefersReducedMotion } from "../../common/use-prefers-reduced-motion";

export interface UseSemanticTransitionOptions {
  /** External low-performance signal (device memory / concurrency / queue). */
  lowPerformance?: boolean;
  /** Optional initial profile override (e.g. from an app-level settings store). */
  initialProfile?: Partial<MotionProfile>;
}

export interface UseSemanticTransitionResult {
  /** Enqueue a projection delta event for animation. */
  enqueue: (event: ProjectionTransitionEvent) => void;
  /** Immediate settle: drop all animation, keep static markers. */
  settleNow: () => void;
  /** Current live queue depth (for debugging / perf gating). */
  queueSize: number;
  /** Resolve a per-entity directive, or null if the entity is not animated. */
  directiveFor: (entityId: string) => MotionDirective | null;
  /** Persistent marker class for an entity (survives animation end). */
  markerFor: (entityId: string) => string | null;
  /** Read-only spatial context for renderer-owned anchor geometry. */
  transitionFor: (
    entityId: string,
  ) => Pick<QueuedEntry, "id" | "verb" | "anchorId" | "relatedIds"> | null;
  readonly profile: MotionProfile;
}

function detectLowPerformance(force?: boolean): boolean {
  if (force !== undefined) return force;
  if (typeof navigator === "undefined") return false;
  const cores = navigator.hardwareConcurrency ?? Infinity;
  const memory = (navigator as { deviceMemory?: number }).deviceMemory ?? Infinity;
  return cores <= 2 || memory <= 4;
}

export function useSemanticTransition(
  options: UseSemanticTransitionOptions = {},
): UseSemanticTransitionResult {
  const prefersReduced = usePrefersReducedMotion();
  const [lowPerformance, setLowPerformance] = useReducer(
    (_unused: boolean, next: boolean) => next,
    detectLowPerformance(options.lowPerformance),
  );

  const reducerInit = useCallback(
    (): TransitionQueue =>
      createTransitionQueue({
        nowMs: 0,
        profile: {
          reducedMotion: options.initialProfile?.reducedMotion ?? prefersReduced,
          lowPerformance:
            options.initialProfile?.lowPerformance ?? detectLowPerformance(options.lowPerformance),
        },
      }),
    [options.initialProfile, prefersReduced, options.lowPerformance],
  );

  const [queue, dispatch] = useReducer(transitionQueueReducer, undefined, reducerInit);
  const queueRef = useRef(queue);
  queueRef.current = queue;

  // React to reduced-motion / low-perf changes: update profile (settles the
  // queue to terminal, preserving static markers).
  useEffect(() => {
    dispatch({
      type: "set-profile",
      profile: { reducedMotion: prefersReduced, lowPerformance },
      nowMs: performance.now(),
    });
  }, [prefersReduced, lowPerformance]);

  useEffect(() => {
    setLowPerformance(detectLowPerformance(options.lowPerformance));
  }, [options.lowPerformance]);

  // Visibility tracking: record hidden, collapse backlog on restore.
  useEffect(() => {
    const onVisibility = () => {
      dispatch({
        type: "set-hidden",
        hidden: document.hidden,
        nowMs: performance.now(),
      });
    };
    onVisibility();
    document.addEventListener("visibilitychange", onVisibility);
    return () => document.removeEventListener("visibilitychange", onVisibility);
  }, []);

  // RAF clock: advance the queue while visible, throttled under low-perf.
  useEffect(() => {
    let raf = 0;
    let lastFrame = 0;
    const step = (nowMs: number) => {
      if (!lowPerformance || nowMs - lastFrame >= MOTION_LOW_PERF_FRAME_MS) {
        lastFrame = nowMs;
        dispatch({ type: "tick", nowMs, isHidden: document.hidden });
      }
      raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [lowPerformance]);

  const enqueue = useCallback((event: ProjectionTransitionEvent) => {
    dispatch({ type: "enqueue", event, nowMs: performance.now() });
  }, []);

  const settleNow = useCallback(() => {
    dispatch({ type: "settle-all", nowMs: performance.now() });
  }, []);

  const queueSize = useMemo(() => liveQueueSize(queue), [queue]);
  const markers = useMemo(() => settledMarkers(queue), [queue]);

  // Resolve a directive for an entity id from the current live entry.
  const directiveFor = useCallback(
    (entityId: string): MotionDirective | null => {
      const live = liveTransitionForEntity(queue, entityId);
      if (!live) return null;
      const opts: MotionDirectiveOptions = {
        reducedMotion: queue.profile.reducedMotion,
        lowPerformance: queue.profile.lowPerformance,
        animationDelayMs: Math.max(0, live.plannedStartMs - performance.now()),
        durationMs: Math.max(0, live.plannedEndMs - live.plannedStartMs),
        displacementScale: queue.profile.lowPerformance ? 0.5 : 1,
        marker: live.marker,
      };
      return buildMotionDirective(live.verb, live.id, opts);
    },
    [queue],
  );

  const markerFor = useCallback(
    (entityId: string): string | null => {
      const marker = markers.get(entityId);
      if (!marker) return null;
      // Rules ①/②: conflict/escalate/stale/revise keep a persistent marker.
      if (marker.marker === "none") return null;
      return `research-motion-marker research-motion-marker-${marker.marker}`;
    },
    [markers],
  );

  const transitionFor = useCallback(
    (entityId: string) => {
      const live = liveTransitionForEntity(queue, entityId);
      if (!live) return null;
      return {
        id: live.id,
        verb: live.verb,
        anchorId: live.anchorId,
        relatedIds: live.relatedIds,
      };
    },
    [queue],
  );

  return {
    enqueue,
    settleNow,
    queueSize,
    directiveFor,
    markerFor,
    transitionFor,
    profile: queue.profile,
  };
}
