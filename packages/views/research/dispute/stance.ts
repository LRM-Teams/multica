"use client";

/**
 * LRM-1472 / UI-04 — stance encoding helpers (glyph + label + tone).
 * Non-color stance anchors: each stance carries a stable glyph and text label
 * so support / contradiction / refinement stay legible grayscale + screen-reader.
 */

import { useT } from "../../i18n/use-t";
import type { PositionView } from "./model";

export type DisputeStanceTone = "success" | "danger" | "warning";

export function stanceGlyph(stance: PositionView["stance"]): string {
  switch (stance) {
    case "contradicts":
      return "✕";
    case "conditional":
      return "⇄";
    case "supports":
    default:
      return "✓";
  }
}

export function stanceTone(stance: PositionView["stance"]): DisputeStanceTone {
  switch (stance) {
    case "contradicts":
      return "danger";
    case "conditional":
      return "warning";
    case "supports":
    default:
      return "success";
  }
}

export function stanceLabel(
  stance: PositionView["stance"],
  t: ReturnType<typeof useT<"research">>["t"],
): string {
  switch (stance) {
    case "contradicts":
      return t(($) => $.dispute.stance.contradicts);
    case "conditional":
      return t(($) => $.dispute.stance.conditional);
    case "supports":
    default:
      return t(($) => $.dispute.stance.supports);
  }
}
