import { z } from "zod";
import type {
  ResearchReportStructured,
  ResearchReportStructuredV1,
} from "../types/research";

export const RESEARCH_REPORT_SCHEMA_VERSION = 1 as const;

const OutlineNodeSchema = z
  .object({
    id: z.string(),
    title: z.string().optional().default(""),
    level: z.number().int().min(1).max(6).optional().default(1),
    children: z.array(z.string()).optional().default([]),
  })
  .passthrough();

const SectionSchema = z
  .object({
    id: z.string(),
    title: z.string().optional().default(""),
    level: z.number().int().min(1).max(6).optional().default(1),
    markdown: z.string().optional().default(""),
    citation_ids: z.array(z.string()).optional().default([]),
  })
  .passthrough();

const CitationSchema = z
  .object({
    id: z.string(),
    index: z.number().int().min(1).optional().default(1),
    source_id: z.string().optional().default(""),
    label: z.string().optional().default(""),
    quote: z.string().optional(),
    locator: z.string().optional(),
  })
  .passthrough();

const SourceRefSchema = z
  .object({
    source_id: z.string(),
    title: z.string().optional().default(""),
    url: z.string().optional().default(""),
    credibility_weight: z.number().nullable().optional(),
    source_class: z.string().optional().default("other"),
  })
  .passthrough();

/** Zod for `report.structured` when schema_version is 1. */
export const ResearchReportStructuredV1Schema = z
  .object({
    schema_version: z.literal(1),
    title: z.string().optional().default(""),
    outline: z.array(OutlineNodeSchema).optional().default([]),
    sections: z.array(SectionSchema).optional().default([]),
    citations: z.array(CitationSchema).optional().default([]),
    sources: z.array(SourceRefSchema).optional().default([]),
    gaps: z.array(z.string()).optional(),
    conclusion: z.string().optional(),
  })
  .passthrough();

export type ReportStructuredRenderMode =
  | "structured"
  | "markdown_only"
  | "readonly_markdown";

export type ReportStructuredKind = "v1" | "legacy_empty" | "unknown";

export interface NormalizedReportStructured {
  kind: ReportStructuredKind;
  /** Parsed v1 payload when kind === "v1"; otherwise null. */
  structured: ResearchReportStructuredV1 | null;
  /**
   * FE render hint:
   * - structured: use outline/sections/citations
   * - markdown_only: legacy empty structured — render content_md; empty outline OK
   * - readonly_markdown: unknown future schema_version — prefer content_md, ignore unknown fields
   */
  render_mode: ReportStructuredRenderMode;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isEmptyStructured(value: unknown): boolean {
  if (value == null) return true;
  if (!isPlainObject(value)) return false;
  return Object.keys(value).length === 0;
}

/**
 * Normalize opaque `report.structured` for FE / exporters.
 * Does not throw — unknown shapes degrade to markdown-only / readonly modes.
 */
export function normalizeReportStructured(raw: unknown): NormalizedReportStructured {
  if (isEmptyStructured(raw)) {
    return { kind: "legacy_empty", structured: null, render_mode: "markdown_only" };
  }
  if (!isPlainObject(raw)) {
    return { kind: "unknown", structured: null, render_mode: "readonly_markdown" };
  }

  const version = raw.schema_version;
  if (version === undefined || version === null) {
    // Pre-contract rows may have ad-hoc keys without schema_version.
    return { kind: "legacy_empty", structured: null, render_mode: "markdown_only" };
  }
  if (version !== RESEARCH_REPORT_SCHEMA_VERSION) {
    return { kind: "unknown", structured: null, render_mode: "readonly_markdown" };
  }

  const parsed = ResearchReportStructuredV1Schema.safeParse(raw);
  if (!parsed.success) {
    return { kind: "unknown", structured: null, render_mode: "readonly_markdown" };
  }

  return {
    kind: "v1",
    structured: parsed.data as ResearchReportStructuredV1,
    render_mode: "structured",
  };
}

/** Type guard for writers that want a typed v1 payload. */
export function isResearchReportStructuredV1(
  value: unknown,
): value is ResearchReportStructured {
  return normalizeReportStructured(value).kind === "v1";
}
