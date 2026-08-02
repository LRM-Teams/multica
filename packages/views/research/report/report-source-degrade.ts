import type {
  ResearchReportCitation,
  ResearchReportSourceRef,
  ResearchSource,
} from "@multica/core/types";
import {
  isCitationSourceDegraded,
  resolveCitationSource,
  type CitationCardSource,
} from "./report-citation-resolve";

/** LRM-834 — payload-derived failure reason codes for readable copy. */
export type SourceFailureReasonCode =
  | "fetch_failed"
  | "timeout"
  | "http"
  | "invalid_url"
  | "missing"
  | "unknown";

function payloadRecord(payload: unknown): Record<string, unknown> | null {
  if (!payload || typeof payload !== "object") return null;
  return payload as Record<string, unknown>;
}

/** True when a live source row should be treated as fetch/scrape failure. */
export function isResearchSourceFailed(
  source: ResearchSource | CitationCardSource | ResearchReportSourceRef | null | undefined,
): boolean {
  return isCitationSourceDegraded(source);
}

/**
 * Resolve a stable reason code for UI copy. Prefers explicit payload flags,
 * then falls back to structural signals (missing / bad URL).
 */
export function resolveSourceFailureReasonCode(
  source: ResearchSource | CitationCardSource | ResearchReportSourceRef | null | undefined,
): SourceFailureReasonCode | null {
  if (!source) return "missing";
  if (!isResearchSourceFailed(source)) return null;

  const payload =
    "payload" in source ? payloadRecord((source as CitationCardSource).payload) : null;
  if (payload) {
    const status = typeof payload.status === "string" ? payload.status.toLowerCase() : "";
    const reason =
      typeof payload.failure_reason === "string"
        ? payload.failure_reason.toLowerCase()
        : typeof payload.error_code === "string"
          ? payload.error_code.toLowerCase()
          : "";
    const blob = `${status} ${reason}`;
    if (blob.includes("timeout") || blob.includes("timed_out")) return "timeout";
    if (blob.includes("http") || blob.includes("status")) return "http";
    if (payload.fetch_failed === true || status === "fetch_failed") return "fetch_failed";
  }

  const url = (source.url ?? "").trim();
  if (url) {
    try {
      // eslint-disable-next-line no-new
      new URL(url);
    } catch {
      return "invalid_url";
    }
  }
  const title = (source.title ?? "").trim();
  if (!title && !url) return "missing";
  return "unknown";
}

/** Optional free-text detail from payload (shown after the Chinese label). */
export function sourceFailureDetail(
  source: ResearchSource | CitationCardSource | null | undefined,
): string | null {
  if (!source || !("payload" in source)) return null;
  const payload = payloadRecord(source.payload);
  if (!payload) return null;
  for (const key of ["failure_message", "error_message", "message", "error", "reason"] as const) {
    const v = payload[key];
    if (typeof v === "string" && v.trim()) return v.trim();
  }
  return null;
}

export function partitionSourcesByFailure(sources: ResearchSource[]): {
  ok: ResearchSource[];
  failed: ResearchSource[];
} {
  const ok: ResearchSource[] = [];
  const failed: ResearchSource[] = [];
  for (const s of sources) {
    if (isResearchSourceFailed(s)) failed.push(s);
    else ok.push(s);
  }
  return { ok, failed };
}

export type SourcesFailureMode = "none" | "partial" | "all";

export function resolveSourcesFailureMode(sources: ResearchSource[]): SourcesFailureMode {
  if (sources.length === 0) return "none";
  const { ok, failed } = partitionSourcesByFailure(sources);
  if (failed.length === 0) return "none";
  if (ok.length === 0) return "all";
  return "partial";
}

/**
 * LRM-834 — drop citations whose resolved source is failed/missing so they
 * never enter the visible citation numbering / footnote sequence.
 */
export function filterCitationsExcludingFailed(
  citations: ResearchReportCitation[],
  liveSources: ResearchSource[],
  structuredSources: ResearchReportSourceRef[] = [],
): ResearchReportCitation[] {
  return citations.filter((citation) => {
    const source = resolveCitationSource(citation, liveSources, structuredSources);
    return !isResearchSourceFailed(source);
  });
}
