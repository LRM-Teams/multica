import type { ResearchReport, ResearchReportCitation, ResearchReportStructuredV1 } from "@multica/core/types";
import { normalizeReportStructured } from "@multica/core/research";
import { safeSourceUrl } from "./safe-source-url";

/**
 * LRM-831 — serialize a report to a complete Markdown document.
 * Prefers the stored `content_md` (source of truth for numbering); otherwise
 * reconstructs from the structured payload so heading hierarchy and citation
 * numbering survive round-tripping.
 */
export function buildReportMarkdown(report: ResearchReport | null | undefined): string {
  if (!report) return "";
  const md = report.content_md?.trim();
  if (md) return md;

  const normalized = normalizeReportStructured(report.structured);
  if (normalized.render_mode !== "structured" || !normalized.structured) return "";
  const structured = normalized.structured;

  const byId = new Map(structured.citations.map((c) => [c.id, c]));
  const parts: string[] = [];
  if (structured.title?.trim()) parts.push(`# ${structured.title.trim()}`, "");
  if (structured.conclusion?.trim()) parts.push(structured.conclusion.trim(), "");

  for (const section of structured.sections) {
    const level = Math.min(Math.max(section.level + 1, 2), 6); // h1 reserved for title
    parts.push(`${"#".repeat(level)} ${section.title}`, "");
    if (section.markdown?.trim()) parts.push(section.markdown.trim(), "");
    for (const citationId of section.citation_ids) {
      const citation = byId.get(citationId);
      if (!citation) continue;
      parts.push(formatCitationFootnote(citation, structured), "");
    }
  }
  return parts.join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

function formatCitationFootnote(citation: ResearchReportCitation, structured: ResearchReportStructuredV1): string {
  const source = structured.sources.find((s) => s.source_id === citation.source_id);
  const label = citation.label || `[${citation.index}]`;
  const rawUrl = source?.url?.trim();
  const url = safeSourceUrl(rawUrl);
  const title = source?.title?.trim() || rawUrl || "Source";
  const line = url ? `${label} [${title}](${url})` : `${label} ${title}`;
  return citation.quote?.trim() ? `${line}\n> ${citation.quote.trim()}` : line;
}
