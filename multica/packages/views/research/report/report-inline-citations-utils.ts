import type { ResearchReportCitation } from "@multica/core/types";

/**
 * LRM-830 — rewrite section markdown: [^n] / [n] tokens matching known
 * citation indexes become [[cit:citation-id]] so ReactMarkdown can project
 * them as clickable inline references. Unknown numbers stay text.
 */
export function rewriteCitationRefs(markdown: string, citations: ResearchReportCitation[]): string {
  if (!markdown || citations.length === 0) return markdown;
  const byIndex = new Map(citations.map((c) => [c.index, c.id]));
  const byLabel = new Map<string, string>();
  for (const c of citations) {
    if (c.label && c.label.trim()) {
      byLabel.set(c.label.replace(/^\[|\]$/g, ""), c.id);
    }
  }
  return markdown.replace(/\[\^?(\d+)\]/g, (whole, rawNum: string) => {
    const num = Number.parseInt(rawNum, 10);
    const id = byIndex.get(num) ?? byLabel.get(rawNum) ?? null;
    if (!id) return whole;
    return `[[cit:${id}]]`;
  });
}
