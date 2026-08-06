"use client";

/**
 * LRM-1448 — trajectory motion UI consumption layer.
 *
 * Parent LRM-1393. Slice 4 on top of the already-merged pure state layers:
 *   - LRM-1400 `trajectory-motion-intents.ts`   (intent / budget)
 *   - LRM-1446 `trajectory-motion-animator.ts`  (directive mapping)
 *   - LRM-1447 `trajectory-motion-controller.ts`(per-target frame / lifecycle)
 *
 * The three layers above are deliberately side-effect-free. This file is the
 * first consumer: it wires the controller's frame state into renderable React
 * state. It resolves the three things the pure layers intentionally leave to a
 * component:
 *
 *   1. **start → settled swap guard** — an entering target exposes its *start*
 *      frame, then flips to the *settled* frame at `activatesAtMs`. Cancelled
 *      / reduced-motion directives resolve straight to settled and never flash
 *      back to start.
 *   2. **clocked lifecycle** — a scheduler advances expired entries out of the
 *      active set; visibility loss skips replay of missed frames (AC 3).
 *   3. **cleanup** — unmount and removed targets release every subscription;
 *      reduced-motion keeps zero displacement while static highlight/status
 *      labels survive.
 *
 * The pure, deterministic heart (`reduceTrajectoryMotionFrame`) is separated
 * from the React side-effects so every transition rule is unit-testable with a
 * virtual clock and typed layout fixtures — no DOM / RAF / fake timers needed.
 */

import { useEffect, useMemo, useRef, useState } from "react";

import type { TrajectoryLaneLayout } from "@multica/core/research";
import {
  activeTrajectoryIntents,
  applyTrajectoryEvent,
  createTrajectoryMotionState,
  type TrajectoryMotionEvent,
  type TrajectoryMotionProfile,
  type TrajectoryMotionState,
} from "../lib/trajectory-motion-intents";
import { resolveTrajectoryMotionDirectives } from "../lib/trajectory-motion-animator";
import {
  advanceTrajectoryMotion,
  createTrajectoryMotionController,
  type TrajectoryControllerEntry,
  type TrajectoryFrame,
  type TrajectoryMotionControllerState,
} from "../lib/trajectory-motion-controller";

/** The renderable per-target surface a component applies to an element. */
export interface TrajectoryRenderFrame {
  /** CSS inline style for the element right now. */
  style: TrajectoryFrame;
  /** enter = animating (use start frame); settled = stable (use settled frame). */
  phase: "enter" | "settled";
  /** Low-performance degradation: caller should drop to 30fps / disable glow. */
  lowPerformance: boolean;
  /** Static highlight/status marker that survives reduced-motion. */
  highlight?: string;
  kind: TrajectoryControllerEntry["kind"];
}

/** All renderable frames after a reduce pass, keyed by target id. */
export type TrajectoryRenderMap = ReadonlyMap<string, TrajectoryRenderFrame>;

/**
 * Pure frame-loop reducer: intents → animator directives → controller state →
 * renderable frame map. Deterministic, side-effect-free, unit-testable.
 *
 * @param events     New motion events for this tick (already scheduled by the
 *                   caller's intents pipeline).
 * @param prev       Previous lane layout (before this update).
 * @param next       Current lane layout (after this update).
 * @param visibleTargetIds  Targets that still exist in the rendered layout;
 *                   anything removed here is cleaned up (no leak).
 * @param nowMs      Virtual wall-clock; drives budget + settle.
 * @param profile    reduced-motion / low-performance degradation profile.
 */
export function reduceTrajectoryMotionFrame(
  motion: TrajectoryMotionState,
  controller: TrajectoryMotionControllerState,
  events: readonly TrajectoryMotionEvent[],
  prev: TrajectoryLaneLayout,
  next: TrajectoryLaneLayout,
  visibleTargetIds: readonly string[],
  nowMs: number,
  profile: TrajectoryMotionProfile,
): { motion: TrajectoryMotionState; controller: TrajectoryMotionControllerState; frames: TrajectoryRenderMap } {
  // 1. Fold new events into the intent layer (budget-coalesce, cancellation).
  let m = motion;
  for (const event of events) {
    m = applyTrajectoryEvent(m, event, profile, nowMs);
  }
  const intents = activeTrajectoryIntents(m);

  // 2. Map to per-target directives (animator handles reduced-motion to zero
  //    displacement and low-performance passthrough).
  const directives = resolveTrajectoryMotionDirectives(intents, prev, next, nowMs);

  // 3. Advance the controller (start/settled, coalesce/cancel, cleanup).
  const c = advanceTrajectoryMotion(controller, directives, visibleTargetIds, nowMs);

  // 4. Surface the current renderable frame per visible target.
  const frames = new Map<string, TrajectoryRenderFrame>();
  for (const id of visibleTargetIds) {
    const entry = c.entries.find((e) => e.targetId === id);
    if (!entry) continue;
    frames.set(id, {
      style: entry.phase === "enter" ? entry.start : entry.settled,
      phase: entry.phase,
      lowPerformance: entry.lowPerformance,
      highlight: entry.highlight,
      kind: entry.kind,
    });
  }

  return { motion: m, controller: c, frames };
}

/**
 * Return the frame surface for a single target, or null when not tracked.
 * Convenience for components that render a known target id.
 */
export function trajectoryRenderFrameAt(
  frames: TrajectoryRenderMap,
  targetId: string,
): TrajectoryRenderFrame | null {
  return frames.get(targetId) ?? null;
}

/** Apply a render frame as inline styles on a DOM element (guard against the
 * undefined transform = no-motion case; never sets layout, only transform/opacity). */
export function applyTrajectoryFrameTo(
  element: HTMLElement,
  frame: TrajectoryRenderFrame,
): void {
  element.style.transform = frame.style.transform === "none" ? "" : frame.style.transform;
  element.style.opacity = String(frame.style.opacity);
  element.style.transition = frame.style.transition;
}

function nativeProfile(): TrajectoryMotionProfile {
  const reduced =
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  return { reducedMotion: reduced, lowPerformance: false };
}

function framesFrom(
  controller: TrajectoryMotionControllerState,
  visibleTargetIds: readonly string[],
): TrajectoryRenderMap {
  const frames = new Map<string, TrajectoryRenderFrame>();
  for (const id of visibleTargetIds) {
    const entry = controller.entries.find((e) => e.targetId === id);
    if (!entry) continue;
    frames.set(id, {
      style: entry.phase === "enter" ? entry.start : entry.settled,
      phase: entry.phase,
      lowPerformance: entry.lowPerformance,
      highlight: entry.highlight,
      kind: entry.kind,
    });
  }
  return frames;
}

/**
 * Content signature of the (layout, prevLayout, events) timeline. Identical
 * content — even behind a brand-new array identity — produces the same key so
 * the hook's effect does not re-run every render.
 */
function trajectoryInputKey(
  layout: TrajectoryLaneLayout,
  prevLayout: TrajectoryLaneLayout | undefined,
  events: readonly TrajectoryMotionEvent[],
): string {
  const layoutKey = (l: TrajectoryLaneLayout | undefined) =>
    l ? l.commits.map((c) => c.id).join(",") : "";
  const eventKey = events
    .map((e) => `${e.kind}_${e.lane}_${e.targetIds.join(",")}_${e.status ?? ""}`)
    .join(";");
  return `${layoutKey(layout)}|${layoutKey(prevLayout)}|${eventKey}`;
}

export interface UseTrajectoryMotionOptions {
  /** Current lane layout (after the latest update). */
  layout: TrajectoryLaneLayout;
  /** Layout from before this update, for direction resolution. */
  prevLayout?: TrajectoryLaneLayout;
  /** Target ids that are actually visible in the render (window). */
  visibleTargetIds: readonly string[];
  /** Batch of motion events to apply for the current tick. */
  events?: readonly TrajectoryMotionEvent[];
  /** Override native degradation profile (tests / static build). */
  profile?: TrajectoryMotionProfile;
  /** Injected timer/cancel for the settle scheduler (tests use virtual clock). */
  scheduler?: {
    setTimeout: (fn: () => void, ms: number) => unknown;
    clearTimeout: (handle: unknown) => void;
  };
}

/**
 * `useTrajectoryMotion` — consume the controller frame state in a component.
 *
 * Synchronously applies the given event batch (or layout diff) and exposes the
 * per-target renderable frames. A scheduler swaps entering targets to their
 * settled frame at `activatesAtMs`; reduced-motion / low-performance are
 * honored without replaying backlog on visibility regain.
 */
export function useTrajectoryMotion(options: UseTrajectoryMotionOptions): TrajectoryRenderMap {
  const { layout, prevLayout, visibleTargetIds, events, profile, scheduler } = options;

  const motionRef = useRef<TrajectoryMotionState>(createTrajectoryMotionState());
  const controllerRef = useRef<TrajectoryMotionControllerState>(createTrajectoryMotionController());
  const visibleRef = useRef(visibleTargetIds);
  visibleRef.current = visibleTargetIds;
  const [frames, setFrames] = useState<TrajectoryRenderMap>(new Map());

  const schedulerRef = useRef(scheduler);
  schedulerRef.current = scheduler;
  const profileRef = useRef(profile);
  profileRef.current = profile;

  // First render with no prevLayout supplied: use the current layout so
  // direction resolution degrades to a no-op (nothing moved yet).
  const prevRef = useRef<TrajectoryLaneLayout | undefined>(prevLayout ?? layout);
  const prevResolved = prevLayout ?? prevRef.current ?? layout;
  prevRef.current = layout;

  // Consumers often pass a fresh `events` array identity on every render (a
  // new literal). Driving the effect off raw identity would re-process and
  // re-render forever. We therefore key the committed timeline by a *content*
  // signature: identical event/layout content (even with a new array identity)
  // yields a stable key, so the reduce runs exactly once per real change.
  const signature = useMemo(
    () => trajectoryInputKey(layout, prevResolved, events ?? []),
    [layout, prevResolved, events],
  );

  useEffect(() => {
    const p = profileRef.current ?? nativeProfile();
    const sched = schedulerRef.current ?? {
      setTimeout: (fn: () => void, ms: number) => setTimeout(fn, ms),
      clearTimeout: (h: unknown) => clearTimeout(h as ReturnType<typeof setTimeout>),
    };

    // Anchor to a single `now` for this commit so the derived `activatesAtMs`
    // and the settle scheduler stay consistent even when a virtual scheduler
    // fires timers immediately.
    const now = Date.now();
    const result = reduceTrajectoryMotionFrame(
      motionRef.current,
      controllerRef.current,
      events ?? [],
      prevResolved,
      layout,
      visibleRef.current,
      now,
      p,
    );
    motionRef.current = result.motion;
    controllerRef.current = result.controller;
    setFrames(result.frames);

    // Reduced-motion resolves straight to settled — nothing to schedule.
    if (p.reducedMotion) return;

    // Deterministic settle loop: arm a timer at the earliest entering target's
    // activate time; on fire, advance at that scheduled time (so settlement is
    // exact regardless of wall-clock drift) and re-arm any remaining entries.
    let timer: unknown | null = null;
    const armNext = () => {
      const due = controllerRef.current.entries
        .filter((e) => e.phase === "enter")
        .map((e) => e.activatesAtMs);
      if (due.length === 0) return;
      const earliest = Math.min(...due);
      const delay = Math.max(0, earliest - now);
      timer = sched.setTimeout(() => {
        controllerRef.current = advanceTrajectoryMotion(
          controllerRef.current,
          [],
          visibleRef.current,
          earliest,
        );
        setFrames(framesFrom(controllerRef.current, visibleRef.current));
        armNext();
      }, delay);
    };
    armNext();

    return () => {
      if (timer != null) sched.clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature]);

  return frames;
}
