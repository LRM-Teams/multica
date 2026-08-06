"use client";

import { useMemo, useState, type JSX, type ReactNode } from "react";
import {
  ResearchCanvasPluginSlotsContext,
  type ResearchCanvasPluginRegistry,
  type ResearchCanvasPluginState,
  type ResearchCanvasPluginSlotsApi,
} from "./registry";
import type { ResearchCanvasPluginRegistration } from "./types";

/**
 * FE-10 — canvas composition host for the six plugin slots.
 *
 * Owns the registry state and exposes a typed mutation API to feature
 * modules. Child slot hosts read the context snapshot and render each slot
 * in its own isolation boundary (suspense + error) so a single module can
 * never blank the canvas.
 *
 * `initialRegistry` lets the canvas (and tests) inject a fresh registry; it
 * defaults to the shared production registry so feature modules and the
 * canvas composition agree on one place.
 */

export interface ResearchCanvasPluginSlotsProps {
  children: ReactNode;
  /** Registry to drive; defaults to the shared production registry. */
  initialRegistry?: ResearchCanvasPluginRegistry;
  /** Seed registrations applied once on mount (composition-side defaults). */
  registrations?: readonly ResearchCanvasPluginRegistration<any>[];
}

export function ResearchCanvasPluginSlots({
  children,
  initialRegistry,
  registrations,
}: ResearchCanvasPluginSlotsProps): JSX.Element {
  const [state, setState] = useState<ResearchCanvasPluginState>(() => {
    const base =
      initialRegistry ?? Object.create(null) as ResearchCanvasPluginRegistry;
    const seeded = { ...base };
    for (const reg of registrations ?? []) {
      seeded[reg.id] = reg;
    }
    return seeded;
  });

  const api = useMemo<ResearchCanvasPluginSlotsApi>(() => {
    return {
      register: (registration) => {
        setState((prev) => ({ ...prev, [registration.id]: registration }));
      },
      remove: (id) => {
        if (!(id in state)) return false;
        setState((prev) => {
          const next = { ...prev };
          delete next[id];
          return next;
        });
        return true;
      },
      has: (id) => id in state,
      forSlot: (slot) =>
        Object.values(state).filter((reg) => reg.slot === slot),
      snapshot: () => state,
    };
  }, [state]);

  const value = useMemo(
    () => ({ registry: state, api }),
    [state, api],
  );

  return (
    <ResearchCanvasPluginSlotsContext.Provider value={value}>
      {children}
    </ResearchCanvasPluginSlotsContext.Provider>
  );
}
