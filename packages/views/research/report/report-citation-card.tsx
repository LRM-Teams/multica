"use client";

import { useState } from "react";
import { ChevronDown, ExternalLink, Locate, Quote } from "lucide-react";
import type {
  ResearchReportCitation,
  ResearchReportSourceRef,
  ResearchSource,
} from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  type CitationCardSource,
  EMPTY_REPORT_SOURCE_REFS,
  citationAnchorId,
  isCitationSourceDegraded,
  resolveCitationSource,
} from "./report-citation-resolve";
import { weightTier } from "./report-weight";

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, "");
  } catch {
    return "";
  }
}

/** Smooth scroll + ~1s highlight fade on the citation's own card (LRM-824). */
function defaultLocate(citationId: string) {
  const el = document.getElementById(citationAnchorId(citationId));
  if (!el) return;
  el.scrollIntoView({ behavior: "smooth", block: "start" });
  el.classList.add("research-anchor-flash");
  window.setTimeout(() => el.classList.remove("research-anchor-flash"), 1000);
}

function WeightChip({ weight }: { weight: number }) {
  const tier = weightTier(weight);
  return (
    <span
      className={cn(
        "inline-flex min-w-[2.5rem] justify-center rounded-md px-1.5 py-0.5 font-mono text-[11px] font-semibold tabular-nums",
        tier === "hi" && "bg-success/15 text-success-strong",
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
  onLocate,
}: {
  citation: ResearchReportCitation;
  source: CitationCardSource | ResearchReportSourceRef | null;
  /** LRM-824 — click the citation number to locate the matching card. */
  onLocate?: (citationId: string) => void;
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
      id={citationAnchorId(citation.id)}
      data-testid="research-citation-card"
      data-citation-id={citation.id}
      data-degraded={degraded ? "true" : "false"}
      className={cn(
        "scroll-mt-4 rounded-lg border bg-muted/15 px-3 py-2.5",
        degraded && "border-dashed border-muted-foreground/35 bg-muted/10",
      )}
    >
      <div className="flex items-start gap-2">
        <button
          type="button"
          data-testid="research-citation-anchor"
          className={cn(
            "mt-0.5 inline-flex shrink-0 items-center gap-0.5 rounded px-1 -mx-1 font-mono text-[11px] font-semibold text-muted-foreground transition-colors",
            "hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30",
          )}
          aria-label={t(($) => $.reader.citation_anchor, { label })}
          onClick={() => {
            // Default locate = self-locate when no handler is supplied; scroll
            // + flash works in any context the card is rendered into.
            const handler = onLocate ?? defaultLocate;
            handler(citation.id);
          }}
        >
          <Locate className="size-3 opacity-70" aria-hidden />
          {label}
        </button>
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

/**
 * LRM-830 — compact citation popover for inline [^n] refs. Shows title, URL
 * (new-window), quote when present, and a "locate footnote" button.
 */
export function InlineFootnoteCard({
  citation,
  source,
  onLocate,
}: {
  citation: ResearchReportCitation;
  source: CitationCardSource | ResearchReportSourceRef | null;
  onLocate?: (citationId: string) => void;
}) {
  const { t } = useT("research");
  const label = citation.label || `[${citation.index}]`;
  const degraded = isCitationSourceDegraded(source);
  const href = !degraded && source?.url ? source.url : null;

  return (
    <div
      data-testid="research-inline-footnote"
      className="rounded-lg border border-brand/25 bg-background/95 p-3 shadow-md backdrop-blur"
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="font-mono text-[11px] font-semibold text-brand">{label}</span>
            {href ? (
              <a
                href={href}
                target="_blank"
                rel="noreferrer noopener"
                className="truncate text-[13px] font-medium text-brand underline-offset-2 hover:underline"
              >
                {source?.title || hostOf(href)}
                <ExternalLink className="ml-1 inline size-3 align-[-1px] opacity-70" aria-hidden />
              </a>
            ) : (
              <span className="truncate text-[13px] font-medium text-muted-foreground">
                {t(($) => $.reader.citation_unavailable)}
              </span>
            )}
          </div>
          {href ? (
            <p className="mt-0.5 truncate text-[11px] text-muted-foreground">{hostOf(href)}</p>
          ) : null}
        </div>
        <button
          type="button"
          data-testid="research-inline-footnote-locate"
          className="inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          aria-label={t(($) => $.reader.citation_anchor, { label })}
          onClick={() => onLocate?.(citation.id)}
        >
          <Locate className="size-3.5" aria-hidden />
        </button>
      </div>
      {citation.quote ? (
        <blockquote className="mt-2 border-l-2 border-brand/50 pl-2 text-[12px] italic text-muted-foreground">
          <Quote className="mr-1 inline size-3 align-[-1px] opacity-60" aria-hidden />
          {citation.quote}
        </blockquote>
      ) : null}
    </div>
  );
}

export function ReportCitationList({
  citations,
  liveSources,
  structuredSources = EMPTY_REPORT_SOURCE_REFS,
  onLocate,
}: {
  citations: ResearchReportCitation[];
  liveSources: ResearchSource[];
  structuredSources?: ResearchReportSourceRef[];
  onLocate?: (citationId: string) => void;
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
          onLocate={onLocate}
        />
      ))}
    </div>
  );
}
