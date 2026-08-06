/**
 * LRM-695 — text-selection Quote/Copy mini-menu support.
 *
 * Pure helpers (no React) so they are trivially unit-testable. The menu UI +
 * DOM listeners live in `components/selection-quote-menu.tsx`.
 *
 * Product spec (UI Designer, #LRM2.0开发群 2026-07-29, Morgan-frozen):
 *  - Select text inside a message → float a 2-item 「引用 / 复制」 menu and
 *    suppress the browser's native long context menu WHILE a selection exists.
 *    No selection → right-click stays native (don't break copy-link etc.).
 *  - Quote: wrap the selection line-by-line in a `>` blockquote, prefix once
 *    with the original author as PLAIN text (never an @mention → no wake),
 *    and append it to the composer via the editor's markdown pipeline. The
 *    caret lands at the end; nothing is sent.
 *  - Copy: copy the selection as plain text (keep newlines) + toast.
 *  - Desktop (fine pointer) only this cut; mobile keeps the OS selection menu.
 *  - Cross-message selection is REJECTED (AC#7 / Frank DM lock 2026-07-29):
 *    both endpoints must live in the SAME message body, or no menu floats.
 */

/** Selector for a message body surface the menu may float over. */
export const MESSAGE_BODY_SELECTOR = "[data-testid='message-body']";

/** The author is stamped on the bubble root for the menu to read (no wake). */
export const MESSAGE_AUTHOR_ATTR = "data-message-author";
export const MESSAGE_ID_ATTR = "data-message-id";

/** Below this viewport width, or on coarse pointers, keep the OS menu (V0). */
export function isFinePointerViewport(): boolean {
  if (typeof window === "undefined") return false;
  if (window.matchMedia?.("(pointer: coarse)").matches) return false;
  return window.innerWidth >= 768;
}

/**
 * Wrap selected text as a Markdown blockquote, prefixing the first line once
 * with the original author as plain text. Multi-line selections keep `>` on
 * every line so the whole selection renders as a single quote block.
 *
 * Trailing blank lines a triple/double-click selection often grabs are trimmed
 * (but intentional internal blank lines are preserved).
 */
export function buildSelectionQuoteMarkdown(author: string | null, text: string): string {
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  while (lines.length > 1 && lines[lines.length - 1] === "") {
    lines.pop();
  }
  const body = lines.length > 0 ? lines : [""];
  const prefix = author && author.trim() ? `${author.trim()}: ` : "";
  const quoted = body.map((line, index) => {
    return index === 0 ? `> ${prefix}${line}` : `> ${line}`;
  });
  return quoted.join("\n");
}

export interface ResolvedMessageSelection {
  /** Selection text, plain (browser ordering, newlines preserved). */
  text: string;
  /** Original author display name (plain text, no mention). */
  author: string | null;
  /** Stable message id of the bubble the selection starts in (if stamped). */
  messageId: string | null;
  /** Viewport-relative rect of the selection range, for menu positioning. */
  rect: DOMRect;
}

function elementFromNode(node: Node | null): Element | null {
  if (!node) return null;
  return node.nodeType === Node.ELEMENT_NODE
    ? (node as Element)
    : node.parentElement;
}

/**
 * Resolve a live `Selection` against a conversation container into the data the
 * Quote/Copy menu needs. Returns null when the selection is collapsed, empty,
 * or not anchored inside a message body within `container` (so the menu never
 * floats over the composer, the sidebar, or other surfaces).
 */
export function resolveMessageSelection(
  selection: Selection | null | undefined,
  container: HTMLElement | null,
): ResolvedMessageSelection | null {
  if (!selection || selection.isCollapsed || selection.rangeCount === 0 || !container) {
    return null;
  }
  const text = selection.toString();
  if (!text.trim()) return null;

  const range = selection.getRangeAt(0);
  // Both endpoints must live inside the conversation container; a selection
  // that bleeds into the composer or outside is ignored.
  if (!container.contains(range.startContainer) || !container.contains(range.endContainer)) {
    return null;
  }

  const startEl = elementFromNode(range.startContainer);
  const endEl = elementFromNode(range.endContainer);
  const startBody = startEl?.closest(MESSAGE_BODY_SELECTOR) ?? null;
  const endBody = endEl?.closest(MESSAGE_BODY_SELECTOR) ?? null;
  if (!startBody || !endBody) return null;

  // AC#7 / Frank DM lock (2026-07-29): cross-message selection is prohibited —
  // both endpoints must live inside the SAME message body. A selection that
  // spans two bubbles is rejected so the menu never floats over a spliced quote
  // (a quote must come from a single message).
  if (startBody !== endBody) return null;

  // The bubble root carries the stamped author / message id (added in
  // channel-message-bubble.tsx). Both endpoints proved to share one message
  // body above, so the anchor bubble is unambiguous.
  const bubble = startEl?.closest(`[${MESSAGE_AUTHOR_ATTR}]`) ?? null;
  const rawAuthor = bubble?.getAttribute(MESSAGE_AUTHOR_ATTR) ?? null;
  const author = rawAuthor && rawAuthor.trim() ? rawAuthor : null;
  const messageId = bubble?.getAttribute(MESSAGE_ID_ATTR) ?? null;

  let rect: DOMRect;
  try {
    rect = range.getBoundingClientRect();
  } catch {
    return null;
  }
  // A zero-height rect (e.g. caret-only) is not a real selection to float over.
  if (rect.width === 0 && rect.height === 0) return null;

  return { text, author, messageId, rect };
}
