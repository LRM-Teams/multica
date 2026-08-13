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
import { safeSourceUrl } from "./safe-source-url";

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
    const detailBits = ["failure_message", "error_message", "message", "error", "reason"]
      .map((k) => payload[k])
      .filter((v): v is string => typeof v === "string")
      .map((v) => v.toLowerCase())
      .join(" ");
    const blob = `${status} ${reason} ${detailBits}`;
    if (
      blob.includes("timeout") ||
      blob.includes("timed_out") ||
      blob.includes("timed out") ||
      blob.includes("etimedout")
    ) {
      return "timeout";
    }
    if (blob.includes("http") || /\bstatus\b/.test(blob) || /\b4\d\d\b/.test(blob) || /\b5\d\d\b/.test(blob)) {
      return "http";
    }
    if (payload.fetch_failed === true || status === "fetch_failed") return "fetch_failed";
  }

  const url = (source.url ?? "").trim();
  if (url && !safeSourceUrl(url)) return "invalid_url";
  const title = (source.title ?? "").trim();
  if (!title && !url) return "missing";
  return "unknown";
}

/**
 * Optional free-text detail from payload.
 * LRM-834: not shown in primary UI — raw codes like ETIMEDOUT are not 中文可读.
 * Kept for diagnostics / future tooltips only.
 */
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

/**
 * Remove `[^n]` / `[n]` tokens for excluded citations so failed/all-failed
 * sources leave no orphan reference marks in Findings prose.
 */
export function stripCitationRefs(
  markdown: string,
  citations: ResearchReportCitation[],
): string {
  if (!markdown || citations.length === 0) return markdown;
  const indexes = new Set(citations.map((c) => c.index));
  const labels = new Set(
    citations
      .map((c) => (c.label || "").replace(/^\[|\]$/g, "").trim())
      .filter(Boolean),
  );
  return markdown
    .replace(/\[\^?([^\]]+)\]/g, (whole, inner: string) => {
      const num = Number.parseInt(inner, 10);
      if (Number.isFinite(num) && String(num) === inner && indexes.has(num)) return "";
      if (labels.has(inner)) return "";
      return whole;
    })
    .replace(/[ \t]+([.,;:!?])/g, "$1")
    .replace(/ {2,}/g, " ")
    .replace(/[ \t]+\n/g, "\n");
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
 * When every live source failed, hide the whole sequence (do not fall back to
 * a structured snapshot that still looks healthy).
 */
export function filterCitationsExcludingFailed(
  citations: ResearchReportCitation[],
  liveSources: ResearchSource[],
  structuredSources: ResearchReportSourceRef[] = [],
): ResearchReportCitation[] {
  const { ok, failed } = partitionSourcesByFailure(liveSources);
  const failedIds = new Set(failed.map((s) => s.id));
  const okIds = new Set(ok.map((s) => s.id));
  const allLiveFailed = liveSources.length > 0 && ok.length === 0;

  return citations.filter((citation) => {
    if (failedIds.has(citation.source_id)) return false;
    if (allLiveFailed) return false;
    if (okIds.has(citation.source_id)) return true;
    const source = resolveCitationSource(citation, liveSources, structuredSources);
    return !isResearchSourceFailed(source);
  });
}
