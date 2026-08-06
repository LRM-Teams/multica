/**
 * Encode / decode Mermaid display mode in the fenced-code info string so the
 * preference survives markdown round-trips (note autosave, page switches).
 *
 * Examples:
 *   mermaid                  → view "both" (default, keeps fence clean)
 *   mermaid view=diagram     → diagram only
 *   mermaid view=source      → source only
 */

export type MermaidViewMode = "both" | "diagram" | "source";

const MERMAID_FENCE_RE =
  /^mermaid(?:\s+view=(diagram|source|both))?(?:\s|$)/i;

export function normalizeMermaidView(value: unknown): MermaidViewMode {
  if (value === "source" || value === "diagram" || value === "both") return value;
  return "both";
}

export function parseCodeFenceInfo(lang: string | null | undefined): {
  language: string;
  mermaidView: MermaidViewMode;
} {
  const raw = (lang ?? "").trim();
  if (!raw) return { language: "", mermaidView: "both" };

  const match = MERMAID_FENCE_RE.exec(raw);
  if (match) {
    return {
      language: "mermaid",
      mermaidView: normalizeMermaidView(match[1] ?? "both"),
    };
  }

  // First token is the language id (CommonMark info string).
  const language = raw.split(/\s+/)[0] ?? raw;
  return { language, mermaidView: "both" };
}

export function serializeCodeFenceInfo(
  language: string | null | undefined,
  mermaidView: MermaidViewMode = "both",
): string {
  const lang = (language ?? "").trim();
  if (lang !== "mermaid") return lang;
  const view = normalizeMermaidView(mermaidView);
  if (view === "both") return "mermaid";
  return `mermaid view=${view}`;
}
