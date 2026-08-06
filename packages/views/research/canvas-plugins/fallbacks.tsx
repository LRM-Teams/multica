"use client";

import type { JSX } from "react";
import { useT } from "../../i18n/use-t";
import { RESEARCH_CANVAS_PLUGIN_SLOT_IDS } from "./types";
import type { ResearchCanvasPluginSlotId } from "./types";

/**
 * FE-10 / LRM-1486 — generic, business-free fallbacks for absent / loading /
 * failed slots.
 *
 * These are the plugin shell's local recovery surface. They are deliberately
 * business-free (no invented projection facts), and each is scoped to ONE
 * slot so it can never blank or lock the whole canvas:
 *
 *  - absent   → a slot with no registered module renders an inert marker;
 *  - loading  → a local skeleton while the lazy chunk resolves (never a
 *               full-canvas lock);
 *  - error    → a small chip naming the plugin with a retry, so a single
 *               failed module is reportable and recoverable.
 *
 * Each fallback carries a stable `data-testid` so the loading/error/absent
 * matrix is observable in tests without depending on business copy.
 */

export function LoadingSlotFallback(): JSX.Element {
  const { t } = useT("research");
  return (
    <div
      data-testid="research-canvas-plugin-loading"
      role="status"
      aria-label={t(($) => $.canvas_plugins.loading_aria)}
      className="pointer-events-none absolute inset-0 flex items-start justify-center pt-2 opacity-0"
    >
      {/* Local skeleton: subtle, inert, never locks the canvas. */}
      <span
        aria-hidden
        className="h-8 w-48 animate-pulse rounded-md border border-dashed border-border bg-muted/30"
      />
    </div>
  );
}

export interface SlotErrorFallbackProps {
  error: Error;
  /** Plugin id (or slot) that failed — reported so the module is identifiable. */
  pluginName: string;
  retry: () => void;
}

export function SlotErrorFallback({
  error,
  pluginName,
  retry,
}: SlotErrorFallbackProps): JSX.Element {
  const { t } = useT("research");
  return (
    <div
      data-testid="research-canvas-plugin-error"
      role="alert"
      className="pointer-events-auto"
    >
      <div className="max-w-64 rounded-md border border-dashed border-destructive/40 bg-card/80 p-2 text-xs text-muted-foreground backdrop-blur-sm">
        <div className="font-medium text-foreground">
          {t(($) => $.canvas_plugins.error_title)}
          <span className="ml-1 font-mono text-[11px] normal-case text-muted-foreground">
            {pluginName}
          </span>
        </div>
        <div className="truncate">
          {error.message || t(($) => $.canvas_plugins.error_unknown)}
        </div>
        <button
          type="button"
          onClick={retry}
          className="mt-1 text-xs underline-offset-2 hover:underline"
        >
          {t(($) => $.canvas_plugins.retry)}
        </button>
      </div>
    </div>
  );
}

/**
 * Business-free placeholder for any absent slot. Inert by default (renders
 * only a marker element) so an unregistered module never alters the canvas
 * look; the marker keeps the absent state observable to tests.
 */
export function AbsentSlotFallback({ slot }: { slot: ResearchCanvasPluginSlotId }): JSX.Element {
  return <span data-testid={`research-canvas-plugin-absent-${slot}`} hidden />;
}

/** Map each slot to its generic absent marker id (for tests/readers). */
export const ABSENT_SLOT_FALLBACK_TEST_IDS = Object.fromEntries(
  RESEARCH_CANVAS_PLUGIN_SLOT_IDS.map((slot) => [
    slot,
    `research-canvas-plugin-absent-${slot}`,
  ]),
) as Record<ResearchCanvasPluginSlotId, string>;
