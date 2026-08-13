import type {
  ResearchReportCitation,
  ResearchReportSourceRef,
  ResearchSource,
} from "@multica/core/types";
import { safeSourceUrl } from "./safe-source-url";

export type CitationCardSource = Pick<
  ResearchSource,
  "id" | "url" | "title" | "credibility_weight" | "summary" | "excerpt" | "payload"
>;

export const EMPTY_RESEARCH_SOURCES: ResearchSource[] = [];
export const EMPTY_REPORT_SOURCE_REFS: ResearchReportSourceRef[] = [];

function isFetchFailedPayload(payload: unknown): boolean {
  if (!payload || typeof payload !== "object") return false;
  const p = payload as Record<string, unknown>;
  return p.fetch_failed === true || p.status === "fetch_failed";
}

/** True when the source row is missing or scrape/fetch failed. */
export function isCitationSourceDegraded(
  source: CitationCardSource | ResearchReportSourceRef | null | undefined,
): boolean {
  if (!source) return true;
  const payload =
    "payload" in source ? (source as CitationCardSource).payload : undefined;
  if (isFetchFailedPayload(payload)) return true;
  const title = (source.title ?? "").trim();
  const url = (source.url ?? "").trim();
  // Empty shell with no recoverable identity → same as fetch failure.
  if (!title && !url) return true;
  if (url && !safeSourceUrl(url)) return true;
  return false;
}

export function resolveCitationSource(
  citation: ResearchReportCitation,
  liveSources: ResearchSource[],
  structuredSources: ResearchReportSourceRef[] = EMPTY_REPORT_SOURCE_REFS,
): CitationCardSource | ResearchReportSourceRef | null {
  const live = liveSources.find((s) => s.id === citation.source_id);
  if (live) return live;
  const snap = structuredSources.find((s) => s.source_id === citation.source_id);
  if (snap) {
    return {
      id: snap.source_id,
      url: snap.url,
      title: snap.title,
      credibility_weight: snap.credibility_weight,
      summary: "",
      excerpt: "",
      payload: {},
    };
  }
  return null;
}

/** DOM id on a citation card, used as a scroll/highlight target (LRM-824). */
export function citationAnchorId(citationId: string): string {
  return `report-citation-${citationId}`;
}
