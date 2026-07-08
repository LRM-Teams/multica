import { useEffect, useMemo, type RefObject } from "react";
import type { VirtuosoHandle } from "react-virtuoso";
import type { ChannelMessage } from "@multica/core/types";
import { toFlatItemIndex } from "../virtuoso-flat-index";

/**
 * #325 phase-2 block 3: deep-link / inline-quote "scroll to and center a specific
 * message" (#309/#303) as a self-contained plugin hook. Owns the target-index
 * derivation and the smooth-scroll effect — a Virtuoso index scroll to bring the
 * row into the window, plus a DOM `scrollIntoView` on the mapped row for
 * sub-item centering. The core list only reads back `highlightIndex` to seed the
 * Virtuoso mount position (`initialTopMostItemIndex`); it holds none of this
 * state and runs none of this effect.
 *
 * The effect deliberately does NOT depend on `messages`: bottom-follow for newly
 * arrived messages is Virtuoso's own `followOutput` job, and re-running on every
 * new message during an open search used to re-fire scrollToIndex repeatedly.
 * (`highlightIndex` already folds in the message identity that matters here.)
 */
export function useHighlightScroll({
  messages,
  highlightMessageId,
  firstItemIndex,
  virtuosoRef,
  messageRefMap,
}: {
  messages: readonly ChannelMessage[];
  highlightMessageId: string | null | undefined;
  firstItemIndex: number;
  virtuosoRef: RefObject<VirtuosoHandle | null>;
  messageRefMap: Map<string, HTMLDivElement>;
}): { highlightIndex: number } {
  const highlightIndex = useMemo(() => {
    if (!highlightMessageId) return -1;
    return messages.findIndex((m) => m.id === highlightMessageId);
  }, [messages, highlightMessageId]);

  useEffect(() => {
    if (!highlightMessageId || highlightIndex < 0) return;
    virtuosoRef.current?.scrollToIndex({
      index: toFlatItemIndex(firstItemIndex, highlightIndex),
      align: "center",
      behavior: "smooth",
    });
    messageRefMap.get(highlightMessageId)?.scrollIntoView({
      block: "center",
      behavior: "smooth",
    });
  }, [highlightMessageId, highlightIndex, firstItemIndex, messageRefMap, virtuosoRef]);

  return { highlightIndex };
}
