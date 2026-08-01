"use client";

import { useState } from "react";
import { ChevronDown, ExternalLink } from "lucide-react";
import type {
  ResearchReportCitation,
  ResearchReportSourceRef,
  ResearchSource,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { weightTier } from "./report-weight";

export type CitationCardSource = Pick<
  ResearchSource,
  "id" | "url" | "title" | "credibility_weight" | "summary" | "excerpt" | "payload"
>;

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, "");
  } catch {
    return "";
  }
}

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
  if (url) {
    try {
      // eslint-disable-next-line no-new
      new URL(url);
    } catch {
      return true;
    }
  }
  return false;
}

export function resolveCitationSource(
  citation: ResearchReportCitation,
  liveSources: ResearchSource[],
  structuredSources: ResearchReportSourceRef[] = [],
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

function WeightChip({ weight }: { weight: number }) {
  const tier = weightTier(weight);
  return (
    <span
      className={cn(
        "inline-flex min-w-[2.5rem] justify-center rounded-md px-1.5 py-0.5 font-mono text-[11px] font-semibold tabular-nums",
        tier === "hi" && "bg-success/15 text-success",
        tier === "mid" && "bg-warning/15 text-warning",
        tier === "lo" && "bg-muted text-muted-foreground",
      )}
    >
      {weight.toFixed(2)}
    </span>
  );
}

export function ReportCitationCard({
  citation,
  source,
}: {
  citation: ResearchReportCitation;
  source: CitationCardSource | ResearchReportSourceRef | null;
}) {
  const { t } = useT("research");
  const [open, setOpen] = useState(false);
  const degraded = isCitationSourceDegraded(source);
  const label = citation.label || `[${citation.index}]`;
  const title = degraded
    ? t(($) => $.reader.citation_unavailable)
    : (source?.title || hostOf(source?.url ?? "") || t(($) => $.reader.citation_untitled));
  const domain = degraded
    ? t(($) => $.reader.citation_fetch_failed)
    : hostOf(source?.url ?? "") || "—";
  const weight = source?.credibility_weight;
  const summary =
    (source && "summary" in source ? source.summary : "") ||
    (source && "excerpt" in source ? source.excerpt : "") ||
    citation.quote ||
    "";
  const href = !degraded && source?.url ? source.url : null;

  return (
    <article
      data-testid="research-citation-card"
      data-citation-id={citation.id}
      data-degraded={degraded ? "true" : "false"}
      className={cn(
        "rounded-lg border bg-muted/15 px-3 py-2.5",
        degraded && "border-dashed border-muted-foreground/35 bg-muted/10",
      )}
    >
      <div className="flex items-start gap-2">
        <span className="mt-0.5 shrink-0 font-mono text-[11px] font-semibold text-muted-foreground">
          {label}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-2">
            {href ? (
              <a
                href={href}
                target="_blank"
                rel="noreferrer noopener"
                className="min-w-0 truncate text-sm font-medium text-brand underline-offset-2 hover:underline"
              >
                {title}
                <ExternalLink className="ml-1 inline size-3 align-[-1px] opacity-70" aria-hidden />
              </a>
            ) : (
              <span className="min-w-0 truncate text-sm font-medium text-muted-foreground">
                {title}
              </span>
            )}
            {typeof weight === "number" && !degraded ? (
              <WeightChip weight={weight} />
            ) : (
              <span className="shrink-0 font-mono text-[11px] text-muted-foreground">—</span>
            )}
          </div>
          <p className="mt-0.5 truncate text-[11px] text-muted-foreground">{domain}</p>
        </div>
        <button
          type="button"
          className="mt-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          aria-expanded={open}
          aria-label={
            open
              ? t(($) => $.reader.citation_collapse)
              : t(($) => $.reader.citation_expand)
          }
          onClick={() => setOpen((v) => !v)}
        >
          <ChevronDown
            className={cn("size-4 transition-transform", open && "rotate-180")}
          />
        </button>
      </div>
      {open ? (
        <div
          data-testid="research-citation-summary"
          className="mt-2 border-t border-border/60 pt-2 text-[13px] leading-relaxed text-muted-foreground"
        >
          {degraded ? (
            <p>{t(($) => $.reader.citation_fetch_failed_hint)}</p>
          ) : summary ? (
            <>
              <p className="whitespace-pre-wrap text-foreground/90">{summary}</p>
              {citation.quote && summary !== citation.quote ? (
                <blockquote className="mt-2 border-l-2 border-brand/50 pl-2 text-[12px] italic">
                  {citation.quote}
                </blockquote>
              ) : null}
            </>
          ) : (
            <p>{t(($) => $.reader.citation_summary_empty)}</p>
          )}
        </div>
      ) : null}
    </article>
  );
}

export function ReportCitationList({
  citations,
  liveSources,
  structuredSources = [],
}: {
  citations: ResearchReportCitation[];
  liveSources: ResearchSource[];
  structuredSources?: ResearchReportSourceRef[];
}) {
  if (citations.length === 0) return null;
  return (
    <div
      data-testid="research-citation-list"
      className="mt-3 space-y-2"
      aria-label="Citations"
    >
      {citations.map((citation) => (
        <ReportCitationCard
          key={citation.id}
          citation={citation}
          source={resolveCitationSource(citation, liveSources, structuredSources)}
        />
      ))}
    </div>
  );
}
