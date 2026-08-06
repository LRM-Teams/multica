"use client";

import { createContext, useContext } from "react";
import type { ResearchCanvasPluginRegistration } from "./types";

/**
 * FE-10 — typed plugin registry.
 *
 * Holds the lazy registrations for the six canvas plugin slots. It ONLY
 * organizes display components: it never touches the committed Projection,
 * never authors canonical nodes, and never imports a feature module's
 * business logic. A registration is a `load` thunk that resolves to a
 * display component; the registry stays empty until a module registers into
 * its slot, and the chunk is only fetched when a slot host actually renders.
 */

export type ResearchCanvasPluginRegistry = Record<
  string,
  ResearchCanvasPluginRegistration<any>
>;

export type ResearchCanvasPluginState = Readonly<ResearchCanvasPluginRegistry>;

/**
 * Create a fresh registry (used by tests and by the canvas default).
 * Prefer the module singleton `defaultResearchCanvasPluginRegistry` in
 * production; a fresh registry lets tests verify isolation without mutating
 * shared global state.
 */
export function createResearchCanvasPluginRegistry(): ResearchCanvasPluginRegistry {
  return Object.create(null) as ResearchCanvasPluginRegistry;
}

/** Shared production registry. */
export const defaultResearchCanvasPluginRegistry: ResearchCanvasPluginRegistry =
  createResearchCanvasPluginRegistry();

export interface ResearchCanvasPluginSlotsApi {
  /** Register a lazy plugin for a slot; keyed by plugin id. */
  register(
    registration: ResearchCanvasPluginRegistration<any>,
  ): void;
  /** Unregister a plugin by id; returns true if it was present. */
  remove(id: string): boolean;
  /** Whether a plugin has been registered for an id. */
  has(id: string): boolean;
  /** All current registrations by slot. */
  forSlot(slot: string): ResearchCanvasPluginRegistration<any>[];
  /** Full registry snapshot (read-only). */
  snapshot(): ResearchCanvasPluginState;
}

export interface ResearchCanvasPluginSlotsContextValue {
  registry: ResearchCanvasPluginState;
  api: ResearchCanvasPluginSlotsApi;
}

/**
 * Context carrying the registry + its mutation API. Feature modules register
 * their lazy display component into the slot they own; the canvas hosts read
 * the snapshot and render isolated slot boundaries.
 */
export const ResearchCanvasPluginSlotsContext =
  createContext<ResearchCanvasPluginSlotsContextValue | null>(null);

export function useResearchCanvasPluginSlots(): ResearchCanvasPluginSlotsContextValue | null {
  return useContext(ResearchCanvasPluginSlotsContext);
}

/** Registry reducer/coalescer used to derive an immutable snapshot. */
export function researchCanvasPluginRegistryReduce(
  state: ResearchCanvasPluginState,
  delta: {
    type: "register";
    registration: ResearchCanvasPluginRegistration<any>;
  } | { type: "remove"; id: string },
): ResearchCanvasPluginState {
  if (delta.type === "remove") {
    if (!(delta.id in state)) return state;
    const next = { ...state };
    delete next[delta.id];
    return next;
  }
  return { ...state, [delta.registration.id]: delta.registration };
}
