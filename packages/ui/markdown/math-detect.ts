/**
 * LRM-1264 R2 — math detection without pulling KaTeX CSS/JS.
 */

/** Cheap heuristic — false negatives only delay math until a remount. */
export function looksLikeMathMarkdown(source: string): boolean {
  if (!source) return false;
  // $$…$$, $…$, \(…\), \[…\]
  return /\$\$[\s\S]+?\$\$|\$[^$\n]+\$|\\\(|\\\[|\\begin\{/.test(source);
}
