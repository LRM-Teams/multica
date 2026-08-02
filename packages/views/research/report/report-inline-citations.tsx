"use client";

import { useCallback, useMemo, useState } from "react";
import type { ResearchReportCitation, ResearchReportSourceRef, ResearchSource } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { InlineFootnoteCard } from "./report-citation-card";
import { EMPTY_REPORT_SOURCE_REFS, resolveCitationSource } from "./report-citation-resolve";
import { rewriteCitationRefs } from "./report-inline-citations-utils";

function InlineCitationRef({
  citationId,
  label,
  citation,
  source,
  onLocate,
}: {
  citationId: string;
  label: string;
  citation: ResearchReportCitation | undefined;
  source: ReturnType<typeof resolveCitationSource>;
  onLocate?: (citationId: string) => void;
}) {
  const { t } = useT("research");
  const [open, setOpen] = useState(false);
  const displayLabel = citation?.label || label;

  return (
    <>
      <button
        type="button"
        data-testid="research-inline-citation"
        data-citation-id={citationId}
        aria-label={t(($) => $.reader.citation_anchor, { label: displayLabel })}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "inline-flex items-center rounded px-1 font-mono text-[11px] font-semibold text-brand",
          "hover:bg-brand/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30",
        )}
      >
        {displayLabel}
      </button>
      {open ? (
        <div className="mt-2" data-testid="research-inline-citation-pop">
          <InlineFootnoteCard
            citation={
              citation ?? {
                id: citationId,
                index: 0,
                source_id: "",
                label: displayLabel,
              }
            }
            source={source}
            onLocate={onLocate}
          />
        </div>
      ) : null}
    </>
  );
}

/**
 * Inline citation renderer for ReportProse markdown. Rewrites [^n]/[n] into
 * [[cit:id]] tokens and projects them as clickable refs (LRM-830).
 */
export function InlineCitations({
  markdown,
  citations,
  liveSources,
  structuredSources = EMPTY_REPORT_SOURCE_REFS,
  onLocateCitation,
  children,
}: {
  markdown: string;
  citations: ResearchReportCitation[];
  liveSources: ResearchSource[];
  structuredSources?: ResearchReportSourceRef[];
  onLocateCitation?: (citationId: string) => void;
  children: (markdown: string, renderCitation: (token: { citationId: string; label: string }) => React.ReactNode) => React.ReactNode;
}) {
  const byId = useMemo(() => new Map(citations.map((c) => [c.id, c])), [citations]);
  const rewritten = useMemo(
    () => rewriteCitationRefs(markdown, citations),
    [markdown, citations],
  );
  const renderCitation = useCallback(
    ({ citationId, label }: { citationId: string; label: string }) => {
      const citation = byId.get(citationId);
      const source = citation
        ? resolveCitationSource(citation, liveSources, structuredSources)
        : null;
      return (
        <InlineCitationRef
          citationId={citationId}
          label={label}
          citation={citation}
          source={source}
          onLocate={onLocateCitation}
        />
      );
    },
    [byId, liveSources, structuredSources, onLocateCitation],
  );
  return <>{children(rewritten, renderCitation)}</>;
}
