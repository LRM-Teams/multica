import type { NormalizedReportStructured } from "@multica/core/research";

export type OutlineItem = {
  id: string;
  title: string;
  level: number;
};

/**
 * Build a flat, display-ordered outline for the delivery reader (LRM-829).
 * Prefers structured outline (+ nested children ids); falls back to sections;
 * markdown_only uses body + sources (two levels). Always appends sources when missing.
 */
export function buildOutlineItems(
  normalized: NormalizedReportStructured,
  labels: { sources: string; body: string },
): OutlineItem[] {
  if (normalized.render_mode === "structured" && normalized.structured) {
    const structured = normalized.structured;
    const byId = new Map(structured.outline.map((n) => [n.id, n]));
    const seen = new Set<string>();
    const out: OutlineItem[] = [];

    const pushNode = (id: string, fallbackLevel: number) => {
      if (seen.has(id)) return;
      const node = byId.get(id);
      const section = structured.sections.find((s) => s.id === id);
      const title = (node?.title || section?.title || id).trim();
      if (!title) return;
      const level = Math.min(
        6,
        Math.max(1, node?.level ?? section?.level ?? fallbackLevel),
      );
      seen.add(id);
      out.push({ id, title, level });
      for (const childId of node?.children ?? []) {
        pushNode(childId, level + 1);
      }
    };

    for (const node of structured.outline) {
      pushNode(node.id, node.level || 1);
    }
    for (const section of structured.sections) {
      if (!seen.has(section.id)) {
        out.push({
          id: section.id,
          title: section.title || section.id,
          level: Math.min(6, Math.max(1, section.level || 1)),
        });
        seen.add(section.id);
      }
    }

    // Guarantee ≥2 outline levels when the payload is flat: nest sections under body.
    const hasNested = out.some((item) => item.level >= 2);
    if (!hasNested && out.length > 0) {
      const nested = out.map((item) => ({ ...item, level: 2 }));
      out.length = 0;
      out.push({ id: "body", title: labels.body, level: 1 });
      out.push(...nested);
    }

    if (!out.some((i) => i.id === "sources")) {
      out.push({ id: "sources", title: labels.sources, level: 1 });
    }
    return out;
  }

  // markdown_only / unknown — two-level shell so the tree is never flat.
  return [
    { id: "body", title: labels.body, level: 1 },
    { id: "sources", title: labels.sources, level: 2 },
  ];
}

/**
 * Pick the outline id whose section top is nearest below the scroll viewport
 * offset (sticky header allowance). Items must be in document order.
 */
export function resolveActiveOutlineId(
  scrollTop: number,
  offsets: { id: string; offsetTop: number }[],
  stickyPx = 72,
): string | null {
  if (offsets.length === 0) return null;
  let active = offsets[0]!.id;
  for (const item of offsets) {
    if (item.offsetTop - stickyPx <= scrollTop) active = item.id;
    else break;
  }
  return active;
}

/** Resolve a section element's DOM id (matches ReportProse / reader anchors). */
export function outlineSectionDomId(id: string): string {
  if (id === "body" || id === "sources") return `report-${id}`;
  return `report-sec-${id}`;
}
