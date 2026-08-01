import type {
  ResearchGraphNode,
  ResearchReport,
  ResearchSource,
} from "@multica/core/types";

export type DimensionStatus = "open" | "covered" | "gap" | "dead";

export type ExplorationQuestion = {
  id: string;
  title: string;
  nodeType: string;
  active?: boolean;
};

export type ExplorationDimension = {
  family: string;
  title: string;
  status: DimensionStatus;
  required?: boolean;
  questions: ExplorationQuestion[];
  findingSummary?: string;
};

export type SourceStrategyLayer = "general" | "domain";

export type SourceStrategyChip = {
  id: string;
  label: string;
  layer: SourceStrategyLayer;
  why?: string;
  samples: { id: string; title: string; url: string }[];
};

export type SourceStrategyModel = {
  chips: SourceStrategyChip[];
  whyLine: string;
  empty: boolean;
};

export type HumanBoundaryModel = {
  aiCeiling: string;
  mustHuman: string;
  matrix: { human: string; ai: string }[];
  empty: boolean;
};

const GENERAL_SOURCE_CLASSES = new Set([
  "web",
  "x",
  "twitter",
  "github",
  "docs",
  "blog",
  "news",
  "community",
  "other",
]);

function asRecord(payload: unknown): Record<string, unknown> {
  if (payload && typeof payload === "object" && !Array.isArray(payload)) {
    return payload as Record<string, unknown>;
  }
  return {};
}

function str(v: unknown): string {
  return typeof v === "string" ? v.trim() : "";
}

export function dimensionFamilyOf(node: ResearchGraphNode): string {
  const p = asRecord(node.payload);
  return (
    str(p.dimension_family) ||
    str(p.dimensionFamily) ||
    str(p.family) ||
    (node.node_type === "goal" ? "goal" : "unclassified")
  );
}

function statusFromNodes(nodes: ResearchGraphNode[]): DimensionStatus {
  const types = new Set(nodes.map((n) => n.node_type));
  if (types.has("dead_end") && !types.has("finding") && !types.has("pivot")) {
    return "dead";
  }
  if (types.has("conflict") || types.has("refuted")) return "gap";
  // Finding/pivot means the family has a covered branch; in-flight probes keep it open.
  if (types.has("probe") || types.has("agent_activity")) return "open";
  if (types.has("finding") || types.has("pivot")) return "covered";
  if (types.has("subquestion")) return "open";
  return "gap";
}

/** Group canvas nodes into dimension-family accordion rows (LRM-889/890). */
export function buildExplorationDimensions(
  nodes: ResearchGraphNode[],
): ExplorationDimension[] {
  const byFamily = new Map<string, ResearchGraphNode[]>();
  for (const node of nodes) {
    if (
      node.node_type === "goal" ||
      node.node_type === "roster_change" ||
      node.node_type === "stage_gate" ||
      node.node_type === "product_round_gate"
    ) {
      continue;
    }
    const fam = dimensionFamilyOf(node);
    const list = byFamily.get(fam) ?? [];
    list.push(node);
    byFamily.set(fam, list);
  }

  const dims: ExplorationDimension[] = [];
  for (const [family, group] of byFamily) {
    const seed =
      group.find((n) => n.node_type === "subquestion") ||
      group.find((n) => str(asRecord(n.payload).title)) ||
      group[0];
    const p = asRecord(seed?.payload);
    const title =
      str(p.dimension_title) ||
      str(p.family_title) ||
      seed?.title ||
      family;
    const questionTypes = new Set([
      "subquestion",
      "probe",
      "finding",
      "dead_end",
      "conflict",
      "pivot",
    ]);
    const questions: ExplorationQuestion[] = [];
    for (const n of group) {
      if (!questionTypes.has(n.node_type)) continue;
      questions.push({
        id: n.id,
        title: n.title || n.node_type,
        nodeType: n.node_type,
        active: n.status === "active" || n.node_type === "probe",
      });
    }
    const finding = group.find((n) => n.node_type === "finding");
    dims.push({
      family,
      title,
      status: statusFromNodes(group),
      required: p.required === true || family === "human_ai_boundary",
      questions,
      findingSummary: finding?.summary || finding?.title,
    });
  }

  // Prefer delivery-critical families first when present.
  const rank = (f: string) => {
    if (f === "human_ai_boundary") return 0;
    if (f === "cost_schedule") return 1;
    if (f === "problem_definition") return 2;
    return 10;
  };
  return dims.toSorted(
    (a, b) => rank(a.family) - rank(b.family) || a.title.localeCompare(b.title),
  );
}

export function buildSourceStrategy(sources: ResearchSource[]): SourceStrategyModel {
  if (!sources.length) {
    return { chips: [], whyLine: "", empty: true };
  }

  const buckets = new Map<string, ResearchSource[]>();
  for (const s of sources) {
    const key = (s.source_class || "other").toLowerCase();
    const list = buckets.get(key) ?? [];
    list.push(s);
    buckets.set(key, list);
  }

  const chips: SourceStrategyChip[] = [...buckets.entries()].map(([cls, rows]) => {
    const layer: SourceStrategyLayer = GENERAL_SOURCE_CLASSES.has(cls)
      ? "general"
      : "domain";
    const whys = rows.flatMap((r) => {
      const why = str(asRecord(r.payload).why) || r.summary;
      return why ? [why] : [];
    });
    return {
      id: cls,
      label: cls,
      layer,
      why: whys[0],
      samples: rows.slice(0, 3).map((r) => ({
        id: r.id,
        title: r.title || r.url || cls,
        url: r.url || "",
      })),
    };
  });

  chips.sort((a, b) => {
    if (a.layer !== b.layer) return a.layer === "general" ? -1 : 1;
    return a.label.localeCompare(b.label);
  });

  const whyLine =
    chips.map((c) => c.why).find(Boolean) ||
    sources.map((s) => s.summary).find(Boolean) ||
    "";

  return { chips, whyLine, empty: false };
}

function extractBoundaryFromText(text: string): HumanBoundaryModel {
  const lines = text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean);

  let aiCeiling = "";
  let mustHuman = "";
  const matrix: { human: string; ai: string }[] = [];

  for (const line of lines) {
    const lower = line.toLowerCase();
    if (!aiCeiling && /(ai\s*上限|仅靠\s*ai|ai-only|ai only ceiling)/i.test(line)) {
      aiCeiling = line.replace(/^[-*•]\s*/, "").replace(/^[^:：]+[:：]\s*/, "");
    }
    if (!mustHuman && /(必须有人|缺人|must[- ]have[- ]human)/i.test(line)) {
      mustHuman = line.replace(/^[-*•]\s*/, "").replace(/^[^:：]+[:：]\s*/, "");
    }
    // crude "人 … | AI …" / "人做 … AI做 …"
    const m =
      line.match(/人[做侧]?[:：]\s*(.+?)\s*[|/｜]\s*AI[做侧]?[:：]\s*(.+)/i) ||
      line.match(/人做[:：]?\s*(.+?)\s*[/／]\s*AI做[:：]?\s*(.+)/i);
    const humanCell = m?.[1]?.trim();
    const aiCell = m?.[2]?.trim();
    if (humanCell && aiCell) matrix.push({ human: humanCell, ai: aiCell });
    void lower;
  }

  const empty = !aiCeiling && !mustHuman && matrix.length === 0;
  return { aiCeiling, mustHuman, matrix, empty };
}

/** Prefer human_ai_boundary findings; fall back to report markdown heuristics. */
export function buildHumanBoundary(
  nodes: ResearchGraphNode[],
  report: ResearchReport | null | undefined,
): HumanBoundaryModel {
  const boundaryNodes = nodes.filter(
    (n) =>
      dimensionFamilyOf(n) === "human_ai_boundary" ||
      /人机|human.?ai|boundary/i.test(`${n.title} ${n.summary}`),
  );
  const blob = [
    ...boundaryNodes.map((n) => `${n.title}\n${n.summary}`),
    report?.content_md ?? "",
  ].join("\n");

  const fromText = extractBoundaryFromText(blob);
  if (!fromText.empty) return fromText;

  // Structured payload optional fields
  for (const n of boundaryNodes) {
    const p = asRecord(n.payload);
    const boundary = asRecord(p.boundary);
    const ai = str(boundary.ai_ceiling) || str(p.ai_ceiling);
    const human = str(boundary.must_human) || str(p.must_human);
    const matrixRaw = boundary.matrix ?? p.matrix;
    const matrix: { human: string; ai: string }[] = [];
    if (Array.isArray(matrixRaw)) {
      for (const row of matrixRaw) {
        const r = asRecord(row);
        if (str(r.human) || str(r.ai)) {
          matrix.push({ human: str(r.human), ai: str(r.ai) });
        }
      }
    }
    if (ai || human || matrix.length) {
      return { aiCeiling: ai, mustHuman: human, matrix, empty: false };
    }
  }

  return { aiCeiling: "", mustHuman: "", matrix: [], empty: true };
}
