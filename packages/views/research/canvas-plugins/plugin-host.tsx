"use client";

import { lazy, memo, Suspense, useMemo, useState, type ComponentType, type ReactNode } from "react";
import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";
import { useResearchCanvasSlot } from "./use-registered-slot";
import {
  AbsentSlotFallback,
  LoadingSlotFallback,
  SlotErrorFallback,
} from "./fallbacks";
import type {
  ResearchCanvasPluginComponent,
  ResearchCanvasPluginRegistration,
  ResearchCanvasPluginSlotsMap,
} from "./types";

/**
 * The five panel/overlay slots rendered through a mounted component host.
 * (The `nodeRenderer` slot is NOT here — it contributes a `Partial<NodeTypes>`
 * and is resolved by the canvas via `useResearchCanvasNodeRenderers`.)
 */
export type ResearchCanvasRenderSlotId =
  | "insight"
  | "dispute"
  | "motion"
  | "executionOverlay"
  | "trajectoryJump";

/**
 * FE-10 / LRM-1486 — per-slot isolation boundary.
 *
 * Renders one render slot's contribution inside its own Suspense + error
 * boundary, so a single module loading slowly (local skeleton), failing to
 * load (error fallback reporting the plugin name) or being absent (generic
 * fallback) can never take down the whole canvas or another slot.
 *
 * The plugin chunk is only requested once `<ResearchCanvasPluginSlot>` mounts
 * with a registered slot — modules stay code-split and are never eagerly
 * imported.
 */

interface ResearchCanvasPluginSlotProps {
  slot: ResearchCanvasRenderSlotId;
  registration?: ResearchCanvasPluginRegistration<any> | null;
  props: ResearchCanvasPluginSlotsMap[ResearchCanvasRenderSlotId];
}

/** Components are only resolved (and thus loaded) when mounted. */
function resolveSlotComponent<T extends ResearchCanvasRenderSlotId>(
  registration: ResearchCanvasPluginRegistration<T>,
): ResearchCanvasPluginComponent<T> {
  return lazy(() =>
    registration.load().then((m) => ({
      default: m.default as unknown as ComponentType<ResearchCanvasPluginSlotsMap[T]>,
    })),
  );
}

function ResearchCanvasPluginSlotInner({
  slot,
  registration,
  props,
}: ResearchCanvasPluginSlotProps): ReactNode {
  // When composed inside `ResearchCanvasPluginSlots` without an explicit
  // registration, resolve the slot's registration from the provider context so
  // the host stays usable both standalone (explicit prop) and wired via the
  // shared registry (single integration point).
  const contextRegistration = useResearchCanvasSlot(slot);
  const resolved = registration ?? contextRegistration;
  // Bumping this after a failed load creates a NEW lazy instance so the chunk
  // loader actually re-runs on retry (React caches a rejected lazy promise
  // unless a fresh lazy is created) — without it, "Retry" would just re-show
  // the same error fallback forever.
  const [retryNonce, setRetryNonce] = useState(0);
  // Bumping `retryNonce` forces a FRESH lazy instance so the chunk loader
  // re-runs on Retry — React caches a rejected lazy promise unless a new
  // lazy is created. It is deliberately consumed below (`void retryNonce`) so
  // the memo legitimately depends on it and re-creates the lazy on retry.
  const SlotView = useMemo(
    () => {
      void retryNonce;
      return resolved ? resolveSlotComponent(resolved as any) : null;
    },
    [resolved, retryNonce],
  );

  if (!SlotView) {
    return <AbsentSlotFallback slot={slot} />;
  }

  return (
    <ErrorBoundary
      resetKeys={[slot, resolved?.id, retryNonce]}
      fallback={({ error, reset }) => (
        <SlotErrorFallback
          error={error}
          pluginName={resolved?.id ?? slot}
          retry={() => {
            // Reset the boundary AND create a fresh lazy instance so the
            // loader re-runs (see retryNonce above).
            setRetryNonce((n) => n + 1);
            reset();
          }}
        />
      )}
    >
      <Suspense fallback={<LoadingSlotFallback />}>
        <SlotView {...(props as ResearchCanvasPluginSlotsMap[ResearchCanvasRenderSlotId])} />
      </Suspense>
    </ErrorBoundary>
  );
}

/**
 * Public host component. Pass the resolved registration (from the slots
 * provider/api) or `undefined` to render the generic absent fallback. Memoized
 * so a stable registration does not re-render sibling slot hosts.
 */
export const ResearchCanvasPluginSlot = memo(ResearchCanvasPluginSlotInner);
