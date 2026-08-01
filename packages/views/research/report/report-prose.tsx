"use client";

import { normalizeReportStructured } from "@multica/core/research";
import type { ResearchReport, ResearchSource } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { Markdown } from "../../common/markdown";
import { ReportCitationList } from "./report-citation-card";

const proseClass = cn(
  "report-prose max-w-none text-[15px] leading-[1.7] text-foreground",
  "[&_h1]:mb-3 [&_h1]:text-[28px] [&_h1]:font-semibold [&_h1]:tracking-tight",
  "[&_h2]:mt-8 [&_h2]:mb-3 [&_h2]:border-t [&_h2]:border-border [&_h2]:pt-5 [&_h2]:text-lg [&_h2]:font-semibold",
  "[&_h3]:mt-5 [&_h3]:mb-2 [&_h3]:text-base [&_h3]:font-semibold",
  "[&_p]:my-3 [&_ul]:my-3 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-3 [&_ol]:list-decimal [&_ol]:pl-5",
  "[&_li]:my-1",
  "[&_blockquote]:my-4 [&_blockquote]:border-l-4 [&_blockquote]:border-brand [&_blockquote]:bg-brand/5 [&_blockquote]:px-4 [&_blockquote]:py-2 [&_blockquote]:text-muted-foreground",
  "[&_a]:text-brand [&_a]:underline-offset-2 hover:[&_a]:underline",
  "[&_table]:my-4 [&_table]:w-full [&_table]:border-collapse [&_table]:overflow-hidden [&_table]:rounded-[10px] [&_table]:border",
  "[&_th]:bg-muted/40 [&_th]:px-3 [&_th]:py-2 [&_th]:text-left [&_th]:text-[11px] [&_th]:font-medium [&_th]:uppercase [&_th]:text-muted-foreground",
  "[&_td]:border-t [&_td]:px-3 [&_td]:py-2.5",
  "[&_tr:nth-child(even)_td]:bg-muted/20",
  "[&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[13px]",
  "[&_pre]:my-4 [&_pre]:overflow-x-auto [&_pre]:rounded-lg [&_pre]:border [&_pre]:bg-muted/40 [&_pre]:p-3",
);

export function ReportProse({
  report,
  sources = [],
}: {
  report: ResearchReport | null | undefined;
  /** Live session sources — preferred when resolving citation.source_id. */
  sources?: ResearchSource[];
}) {
  if (!report) {
    return <p className="text-sm text-muted-foreground">—</p>;
  }

  const normalized = normalizeReportStructured(report.structured);

  if (normalized.render_mode === "structured" && normalized.structured) {
    const structured = normalized.structured;
    const byId = new Map(structured.citations.map((c) => [c.id, c]));

    return (
      <div className={proseClass}>
        {structured.title ? <h1>{structured.title}</h1> : null}
        {structured.conclusion ? (
          <p className="text-base text-muted-foreground">{structured.conclusion}</p>
        ) : null}
        {structured.sections.map((section) => {
          const Heading = (
            section.level <= 1 ? "h2" : section.level === 2 ? "h3" : "h4"
          ) as "h2" | "h3" | "h4";
          const sectionCitations = section.citation_ids
            .map((id) => byId.get(id))
            .filter((c): c is NonNullable<typeof c> => Boolean(c));
          return (
            <section key={section.id} id={`report-sec-${section.id}`} className="scroll-mt-4">
              <Heading>{section.title}</Heading>
              {section.markdown ? (
                <Markdown mode="full">{section.markdown}</Markdown>
              ) : null}
              <ReportCitationList
                citations={sectionCitations}
                liveSources={sources}
                structuredSources={structured.sources}
              />
            </section>
          );
        })}
        {!structured.sections.length && report.content_md ? (
          <Markdown mode="full">{report.content_md}</Markdown>
        ) : null}
      </div>
    );
  }

  return (
    <div className={proseClass}>
      {report.content_md ? (
        <Markdown mode="full">{report.content_md}</Markdown>
      ) : (
        <p className="text-sm text-muted-foreground">—</p>
      )}
    </div>
  );
}
