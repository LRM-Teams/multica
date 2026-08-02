import type { KeyboardEvent } from "react";
import type { ChannelMessageSearchResult } from "@multica/core/types";

/**
 * LRM-753 / LRM-728 — API returns hits oldest-first (`ORDER BY seq ASC`).
 * Product wants "出结果即跳最近命中", so FE presents newest-first and starts
 * at index 0. Enter advances toward older hits; Shift+Enter goes newer.
 */
export function orderConvSearchResultsNewestFirst(
  results: ChannelMessageSearchResult[],
): ChannelMessageSearchResult[] {
  if (results.length <= 1) return results;
  return [...results].reverse();
}

export function handleConvSearchInputKeyDown(
  event: KeyboardEvent<HTMLInputElement>,
  options: {
    total: number;
    onClose: () => void;
    onNext: () => void;
    onPrev: () => void;
  },
): void {
  if (event.key === "Escape") {
    event.preventDefault();
    options.onClose();
    return;
  }
  if (event.key !== "Enter") return;
  // type=search + Enter must not submit / clear; bind find-next / find-prev.
  event.preventDefault();
  if (options.total <= 0) return;
  if (event.shiftKey) {
    options.onPrev();
    return;
  }
  options.onNext();
}
