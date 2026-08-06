"use client";

import { useResearchCanvasPluginSlots } from "./registry";
import type { ResearchCanvasPluginRegistration, ResearchCanvasPluginSlotsMap } from "./types";

/**
 * Read the registration currently contributed to a slot, or `undefined` when
 * the slot is absent. The host uses this to pick the lazy component to render.
 */
export function useResearchCanvasSlot<T extends keyof ResearchCanvasPluginSlotsMap>(
  slot: T,
): ResearchCanvasPluginRegistration<T> | undefined {
  const slots = useResearchCanvasPluginSlots();
  if (!slots) return undefined;
  const entries = slots.api.forSlot(slot);
  if (entries.length === 0) return undefined;
  return entries[0] as ResearchCanvasPluginRegistration<T>;
}
