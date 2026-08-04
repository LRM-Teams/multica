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
  /** Short blurb for result-card rows (LRM-975). */
  summary?: string;
};

export type ExplorationDimension = {
  family: string;
  title: string;
  status: DimensionStatus;
  required?: boolean;
  questions: ExplorationQuestion[];
  findingSummary?: string;
};

/** Rail body mode when the dimension list is empty or errored (LRM-975). */
export type ExplorationRailMode = "ready" | "loading" | "empty" | "error";

export function resolveExplorationRailMode(
  dimensions: ExplorationDimension[],
  sessionStatus?: string | null,
  error?: string | null,
): ExplorationRailMode {
  if (error) return "error";
  if (dimensions.length > 0) return "ready";
  // In-flight sessions should show generating skeletons — not the old gray kickoff stub.
  if (sessionStatus === "running" || sessionStatus === "paused") return "loading";
  return "empty";
}

function summaryFromNode(node: ResearchGraphNode | undefined): string {
  if (!node) return "";
  return str(node.summary) || str(node.title);
}

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

/**
 * Source strip body mode (LRM-977 / LRM-1282).
 * `partial` = in-flight session that already has real chips — keep facts visible.
 */
export type SourceStrategyMode =
  | "ready"
  | "partial"
  | "loading"
  | "empty"
  | "error";

function isSessionInFlight(sessionStatus?: string | null): boolean {
  return sessionStatus === "running" || sessionStatus === "paused";
}

export function resolveSourceStrategyMode(
  model: SourceStrategyModel,
  sessionStatus?: string | null,
  error?: string | null,
): SourceStrategyMode {
  if (error) return "error";
  const hasFacts = !model.empty && model.chips.length > 0;
  if (hasFacts) {
    return isSessionInFlight(sessionStatus) ? "partial" : "ready";
  }
  if (isSessionInFlight(sessionStatus)) return "loading";
  return "empty";
}

export type HumanBoundaryModel = {
  aiCeiling: string;
  mustHuman: string;
  matrix: { human: string; ai: string }[];
  empty: boolean;
};

/**
 * Boundary panel mode (LRM-978 / LRM-1282).
 * `partial` = in-flight session that already has real boundary facts.
 */
export type HumanBoundaryMode =
  | "ready"
  | "partial"
  | "loading"
  | "empty"
  | "error";

export function resolveHumanBoundaryMode(
  model: HumanBoundaryModel,
  sessionStatus?: string | null,
  error?: string | null,
): HumanBoundaryMode {
  if (error) return "error";
  if (!model.empty) {
    return isSessionInFlight(sessionStatus) ? "partial" : "ready";
  }
  if (isSessionInFlight(sessionStatus)) return "loading";
  return "empty";
}

/**
 * Drawer-level evidence overview (LRM-1325 / LRM-1329).
 * Unique resolver — source/boundary cards must not invent a second overview.
 *
 * Priority: permission → error → ready → partial → loading → empty.
 */
export type EvidenceOverviewMode =
  | "ready"
  | "partial"
  | "loading"
  | "empty"
  | "error"
  | "permission";

export function sourceStrategyHasFacts(model: SourceStrategyModel): boolean {
  return !model.empty && model.chips.length > 0;
}

export function humanBoundaryHasFacts(model: HumanBoundaryModel): boolean {
  return !model.empty;
}

export function resolveEvidenceOverviewMode(input: {
  sourceModel: SourceStrategyModel;
  boundaryModel: HumanBoundaryModel;
  sessionStatus?: string | null;
  error?: string | null;
  /** HTTP status when known; 403 maps to permission (no data leak). */
  errorStatus?: number | null;
}): EvidenceOverviewMode {
  if (input.errorStatus === 403) return "permission";
  if (input.error) return "error";
  const hasSource = sourceStrategyHasFacts(input.sourceModel);
  const hasBoundary = humanBoundaryHasFacts(input.boundaryModel);
  const inFlight = isSessionInFlight(input.sessionStatus);
  if (hasSource && hasBoundary && !inFlight) return "ready";
  if (hasSource || hasBoundary) return "partial";
  if (inFlight) return "loading";
  return "empty";
}

/** Stable revision token for one-shot ready/update sweep (LRM-1329). */
export function evidenceRevisionKey(
  sourceModel: SourceStrategyModel,
  boundaryModel: HumanBoundaryModel,
): string {
  return [
    sourceModel.chips.map((c) => `${c.id}:${c.samples.length}`).join(","),
    boundaryModel.aiCeiling,
    boundaryModel.mustHuman,
    String(boundaryModel.matrix.length),
  ].join("|");
}

/** Duck-typed HTTP status from API/query errors (no ApiError import). */
export function readErrorStatus(error: unknown): number | null {
  if (!error || typeof error !== "object") return null;
  const status = (error as { status?: unknown }).status;
  return typeof status === "number" ? status : null;
}

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
      const qSummary = str(n.summary);
      questions.push({
        id: n.id,
        title: n.title || n.node_type,
        nodeType: n.node_type,
        active: n.status === "active" || n.node_type === "probe",
        summary: qSummary || undefined,
      });
    }
    const finding = group.find((n) => n.node_type === "finding");
    const probe = group.find((n) => n.node_type === "probe");
    const pivot = group.find((n) => n.node_type === "pivot");
    const conflict = group.find((n) => n.node_type === "conflict");
    const dead = group.find((n) => n.node_type === "dead_end");
    const findingSummary =
      summaryFromNode(finding) ||
      summaryFromNode(pivot) ||
      summaryFromNode(conflict) ||
      summaryFromNode(dead) ||
      summaryFromNode(probe) ||
      questions.find((q) => q.summary)?.summary ||
      undefined;
    dims.push({
      family,
      title,
      status: statusFromNodes(group),
      required: p.required === true || family === "human_ai_boundary",
      questions,
      findingSummary,
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
