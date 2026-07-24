import type { MessagePart } from "@multica/core/types";

/**
 * A structured `reference` message part (mention / issue-ref) — the shared
 * inline-token payload. #588/#463 anchor each parsed occurrence to a precise
 * content span so the FE can render it as an inline token WITHOUT searching the
 * text or guessing (which would re-introduce the `mention://` sniffing we're
 * retiring).
 */
export type ReferencePart = Extract<MessagePart, { type: "reference" }>;

/**
 * One run of a message body: a plain-text stretch, or a reference occurrence
 * (carrying the structured part + the exact display substring at its span).
 * The renderer maps `text` runs through the normal text/markdown path and
 * `reference` runs through the shared token renderer (mention / issue-ref /
 * system @handle all consume this ONE projection — Parker/Barry 2026-07-15).
 *
 * `emphasis` (#635): each text run is markdown-parsed INDEPENDENTLY of its
 * neighbors, so an emphasis marker pair that straddles a reference span (the
 * author wrote `**LRM-188**`) never has both halves in the same run — remark
 * sees a lone unmatched `**` on each side and leaves it as literal asterisks.
 * `stripStraddlingEmphasisMarkers` detects exactly this "marker immediately
 * touches the span on both sides" shape and moves it here so the renderer can
 * apply the emphasis directly to the token instead of feeding it back through
 * a second markdown parse.
 */
export type InlineSegment =
  | { kind: "text"; text: string }
  | { kind: "reference"; ref: ReferencePart; text: string; emphasis?: "strong" | "em" };

// Longer markers first — `**`/`__` must be tried before `*`/`_` so "**" isn't
// mistaken for two single-emphasis markers.
const EMPHASIS_MARKERS: ReadonlyArray<{ marker: string; kind: "strong" | "em" }> = [
  { marker: "**", kind: "strong" },
  { marker: "__", kind: "strong" },
  { marker: "*", kind: "em" },
  { marker: "_", kind: "em" },
];

/**
 * Fold a `**`/`__`/`*`/`_` pair that directly touches a reference span (no
 * intervening whitespace, matching CommonMark's own left/right-flanking rule)
 * into that segment's `emphasis`, stripping the marker text from the
 * surrounding runs. Only a marker that appears at the VERY end of the
 * preceding run and the VERY start of the following run counts — anything
 * else is an ordinary mid-sentence marker that the per-run markdown pass
 * already handles correctly on its own.
 */
function stripStraddlingEmphasisMarkers(segments: InlineSegment[]): InlineSegment[] {
  const result = [...segments];
  for (let i = 1; i < result.length - 1; i++) {
    const seg = result[i];
    const prev = result[i - 1];
    const next = result[i + 1];
    if (!seg || seg.kind !== "reference" || !prev || prev.kind !== "text" || !next || next.kind !== "text") {
      continue;
    }

    for (const { marker, kind } of EMPHASIS_MARKERS) {
      if (!prev.text.endsWith(marker) || !next.text.startsWith(marker)) continue;
      result[i - 1] = { kind: "text", text: prev.text.slice(0, -marker.length) };
      result[i] = { kind: "reference", ref: seg.ref, text: seg.text, emphasis: kind };
      result[i + 1] = { kind: "text", text: next.text.slice(marker.length) };
      break;
    }
  }
  // The fold can leave an empty run when the marker was the entire
  // preceding/following text (e.g. content starts with "**"+ref) — every
  // other path in this module already guarantees no empty text segments, so
  // preserve that invariant here too.
  return result.filter((seg) => seg.kind !== "text" || seg.text !== "");
}

function hasSpan(
  part: MessagePart,
): part is ReferencePart & { content_start_utf16: number; content_end_utf16: number } {
  return (
    part.type === "reference" &&
    typeof part.content_start_utf16 === "number" &&
    typeof part.content_end_utf16 === "number"
  );
}

/**
 * Split a message body `content` into ordered text + reference segments using
 * the server-resolved UTF-16 spans on each `reference` part. Because JS strings
 * are already UTF-16, `content_start/end_utf16` index the string directly.
 *
 * Defensive by construction (the projector NEVER trusts a span blindly, so a
 * bad anchor degrades to plain text rather than corrupting the row):
 * - spans out of `[0, content.length]` or with `start >= end` are dropped;
 * - overlapping spans keep the earliest and drop the rest;
 * - references without a span (e.g. un-migrated historical rows) are ignored
 *   here — the bare readable text still renders, no legacy `mention://` reader.
 *
 * Returns a single text segment (or none) when there are no valid anchors, so
 * callers can cheaply detect "nothing to tokenize" and fall back to the plain
 * text/markdown path.
 */
export function projectInlineReferences(
  content: string | null | undefined,
  parts: readonly MessagePart[] | null | undefined,
): InlineSegment[] {
  if (!content) return [];
  const anchored = (parts ?? [])
    .filter(hasSpan)
    .filter(
      (p) =>
        p.content_start_utf16 >= 0 &&
        p.content_end_utf16 <= content.length &&
        p.content_start_utf16 < p.content_end_utf16,
    )
    .sort((a, b) => a.content_start_utf16 - b.content_start_utf16);

  if (anchored.length === 0) {
    return [{ kind: "text", text: content }];
  }

  const segments: InlineSegment[] = [];
  let cursor = 0;
  for (const ref of anchored) {
    // Overlap guard: a span that starts before the previous one ended is
    // dropped (keep the earliest, never render the same characters twice).
    if (ref.content_start_utf16 < cursor) continue;
    if (ref.content_start_utf16 > cursor) {
      segments.push({ kind: "text", text: content.slice(cursor, ref.content_start_utf16) });
    }
    segments.push({
      kind: "reference",
      ref,
      text: content.slice(ref.content_start_utf16, ref.content_end_utf16),
    });
    cursor = ref.content_end_utf16;
  }
  if (cursor < content.length) {
    segments.push({ kind: "text", text: content.slice(cursor) });
  }
  return stripStraddlingEmphasisMarkers(segments);
}
