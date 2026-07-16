import { Extension } from "@tiptap/core";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import type { EditorState, Transaction } from "@tiptap/pm/state";
import { detectLinks } from "@multica/ui/markdown/linkify";

/**
 * Word-boundary autolink (task #531).
 *
 * Replaces `@tiptap/extension-link`'s built-in `autolink` (which we disable).
 * The built-in fires incrementally as you type and maps the matched URL back to
 * document positions with `String.lastIndexOf` — a text-search that desyncs
 * from real ProseMirror positions (the exact pattern #463 bans). Under
 * character-by-character typing that produced split marks and a mangled,
 * phishing-shaped href (see #531 root cause). `inclusive: false` then froze the
 * premature partial mark, so the fix is NOT to touch `inclusive` but to stop
 * marking mid-word.
 *
 * Instead we linkify ONE-SHOT at a word boundary (whitespace / Enter), which is
 * exactly what the built-in intended — only here the word's range is computed
 * from real document positions (walking back from the boundary), never from a
 * text search. Detection reuses the shared `detectLinks` (post #650, host
 * boundaries are clean). The href is normalized in this writer only: an
 * explicit scheme is preserved (http stays http); a scheme-less host gets an
 * explicit `https://` prefix here (NOT inherited from a linkify default, which
 * would emit `http://`).
 *
 * IME-safe: never runs while `view.composing` (between compositionstart and
 * compositionend), so it can't fire on an intermediate pinyin/kana state.
 */

const SCHEME_RE = /^[a-z][a-z0-9+.-]*:\/\//i;

/** Detect a whole-word URL and return its normalized href, or null. */
export function wordHref(word: string): string | null {
  const links = detectLinks(word);
  if (links.length !== 1) return null;
  const link = links[0];
  if (!link || link.type !== "url") return null;
  // Must span the entire word — a partial match means this isn't a bare URL.
  if (link.start !== 0 || link.end !== word.length) return null;
  // Writer-only scheme normalization: explicit scheme preserved, scheme-less
  // host gets https (never the linkify default of http).
  return SCHEME_RE.test(link.text) ? link.text : `https://${link.text}`;
}

/**
 * Walk back from `wordEnd` through document positions (no text search) to the
 * start of the word, stopping at whitespace, an inline atom (mention/image),
 * or the textblock start. Returns [from, wordEnd) or null if empty.
 */
function wordRangeEndingAt(state: EditorState, wordEnd: number): { from: number; to: number } | null {
  if (wordEnd <= 0) return null;
  // Resolve a position INSIDE the word (wordEnd - 1). wordEnd may sit on a block
  // boundary (Enter case), where resolve(wordEnd) is ambiguous between blocks.
  const blockStart = state.doc.resolve(wordEnd - 1).start();
  let from = wordEnd;
  while (from > blockStart) {
    const ch = state.doc.textBetween(from - 1, from, undefined, "￼");
    if (ch === "" || ch === "￼" || /\s/.test(ch)) break;
    from--;
  }
  if (from >= wordEnd) return null;
  return { from, to: wordEnd };
}

/**
 * Apply a link mark over the URL inside the word [wordFrom, wordTo). Uses the
 * detector's real `[start, end)` span (post-#650, host boundaries are clean),
 * NOT a whole-word requirement — so trailing punctuation (`https://x.com,`) or
 * a glued CJK particle (`https://x.com吗`) that the detector correctly excludes
 * stays outside the link instead of dropping the whole match.
 */
function markBareUrl(state: EditorState, wordFrom: number, wordTo: number): Transaction | null {
  const linkType = state.schema.marks.link;
  if (!linkType) return null;
  if (state.doc.resolve(wordFrom).parent.type.spec.code) return null;

  const word = state.doc.textBetween(wordFrom, wordTo);
  const urls = detectLinks(word).filter((l) => l.type === "url");
  if (urls.length !== 1) return null;
  const link = urls[0]!;
  // The word is a contiguous text run (walk-back stopped at any inline atom),
  // so document position = wordFrom + string offset — no text search.
  const from = wordFrom + link.start;
  const to = wordFrom + link.end;
  if (from >= to) return null;
  if (state.doc.rangeHasMark(from, to, linkType)) return null;

  // Writer-only scheme normalization: explicit scheme preserved (http stays
  // http), scheme-less host gets https (never the linkify default of http).
  const href = SCHEME_RE.test(link.text) ? link.text : `https://${link.text}`;
  return state.tr.addMark(from, to, linkType.create({ href }));
}

/**
 * Linkify the word that just ended because a whitespace boundary was typed at
 * `cursorPos` (the position right after the whitespace). Returns a transaction
 * or null.
 */
export function autolinkWordAt(state: EditorState, cursorPos: number): Transaction | null {
  const boundaryPos = cursorPos - 1; // the whitespace we just typed
  if (boundaryPos <= 0) return null;
  const boundaryChar = state.doc.textBetween(boundaryPos, cursorPos, undefined, "￼");
  if (!/\s/.test(boundaryChar)) return null;
  const range = wordRangeEndingAt(state, boundaryPos);
  return range ? markBareUrl(state, range.from, range.to) : null;
}

/**
 * Finalization: linkify the word ending exactly at the cursor (no trailing
 * whitespace needed). Run just before submit so a URL that is the last thing
 * typed ("参见 https://x.com" then send) still becomes a link — the "input end
 * is itself a word boundary" case (#531 acceptance).
 */
export function autolinkFinalWord(state: EditorState | undefined | null): Transaction | null {
  if (!state?.selection) return null;
  const sel = state.selection;
  if (!sel.empty) return null;
  const range = wordRangeEndingAt(state, sel.from);
  return range ? markBareUrl(state, range.from, range.to) : null;
}

export function createWordBoundaryAutolink() {
  return Extension.create({
    name: "wordBoundaryAutolink",
    addProseMirrorPlugins() {
      const editor = this.editor;
      return [
        new Plugin({
          key: new PluginKey("wordBoundaryAutolink"),
          appendTransaction: (transactions, _oldState, newState) => {
            if (!transactions.some((tr) => tr.docChanged)) return null;
            // Never fire mid-composition (IME) or when a command asked us not to.
            if (editor.view?.composing) return null;
            if (transactions.some((tr) => tr.getMeta("preventAutolink"))) return null;
            const sel = newState.selection;
            if (!sel.empty) return null;

            // Case A: a whitespace boundary was just typed at the cursor.
            const byWhitespace = autolinkWordAt(newState, sel.from);
            if (byWhitespace) return byWhitespace;

            // Case B: a block boundary (Enter / paragraph split) — the previous
            // block's trailing word just ended. The cursor now sits at the
            // start of the new block, so link the word ending at the prior
            // block boundary.
            const $cursor = sel.$from;
            if ($cursor.parentOffset === 0 && $cursor.depth > 0) {
              // The previous block's content ends one position before the new
              // block's boundary ($cursor.before() points past its close token).
              const range = wordRangeEndingAt(newState, $cursor.before() - 1);
              if (range) return markBareUrl(newState, range.from, range.to);
            }
            return null;
          },
          props: {
            handleDOMEvents: {
              // IME recovery: when composition ends, a URL that was skipped
              // while composing (we never fire mid-composition) gets linked
              // now. Deferred to a microtask so ProseMirror has committed the
              // composed text into the document first.
              compositionend: (view) => {
                queueMicrotask(() => {
                  if (view.isDestroyed) return;
                  const tr = autolinkFinalWord(view.state);
                  if (tr) view.dispatch(tr);
                });
                return false;
              },
            },
          },
        }),
      ];
    },
  });
}
