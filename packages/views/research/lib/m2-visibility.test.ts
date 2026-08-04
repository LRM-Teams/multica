// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphNode, ResearchSource } from "@multica/core/types";
import {
  buildExplorationDimensions,
  buildHumanBoundary,
  buildSourceStrategy,
  evidenceRevisionKey,
  resolveEvidenceOverviewMode,
  resolveExplorationRailMode,
  resolveHumanBoundaryMode,
  resolveSourceStrategyMode,
} from "./m2-visibility";

function node(
  partial: Partial<ResearchGraphNode> & Pick<ResearchGraphNode, "id" | "node_type">,
): ResearchGraphNode {
  return {
    session_id: "s",
    title: partial.title ?? partial.id,
    summary: partial.summary ?? "",
    status: partial.status ?? "active",
    actor_agent_id: null,
    payload: partial.payload ?? {},
    confidence: null,
    created_at: "",
    updated_at: "",
    ...partial,
  };
}

describe("m2-visibility", () => {
  it("groups dimension families with status dots", () => {
    const dims = buildExplorationDimensions([
      node({
        id: "q1",
        node_type: "subquestion",
        title: "引擎约束",
        payload: { dimension_family: "feasibility", required: true },
      }),
      node({
        id: "f1",
        node_type: "finding",
        title: "可用",
        summary: "WebGL ok",
        payload: { dimension_family: "feasibility" },
      }),
      node({
        id: "c1",
        node_type: "subquestion",
        title: "成本",
        payload: { dimension_family: "cost_schedule" },
      }),
    ]);
    expect(dims.map((d) => d.family)).toContain("feasibility");
    expect(dims.find((d) => d.family === "feasibility")?.status).toBe("covered");
    expect(dims.find((d) => d.family === "feasibility")?.findingSummary).toBe(
      "WebGL ok",
    );
    expect(dims.find((d) => d.family === "cost_schedule")?.status).toBe("open");
  });

  it("resolves rail empty vs in-flight loading vs ready (LRM-975)", () => {
    expect(resolveExplorationRailMode([], "drafting")).toBe("empty");
    expect(resolveExplorationRailMode([], "running")).toBe("loading");
    expect(resolveExplorationRailMode([], "paused")).toBe("loading");
    expect(resolveExplorationRailMode([], "running", "boom")).toBe("error");
    expect(
      resolveExplorationRailMode(
        [
          {
            family: "feasibility",
            title: "可行性",
            status: "open",
            questions: [],
          },
        ],
        "running",
      ),
    ).toBe("ready");
  });

  it("resolves source strip five-state matrix (LRM-977 / LRM-1282)", () => {
    const empty = { chips: [], whyLine: "", empty: true };
    const ready = {
      empty: false,
      whyLine: "why",
      chips: [
        {
          id: "docs",
          label: "docs",
          layer: "general" as const,
          samples: [],
        },
      ],
    };
    expect(resolveSourceStrategyMode(empty, "drafting")).toBe("empty");
    expect(resolveSourceStrategyMode(empty, "running")).toBe("loading");
    expect(resolveSourceStrategyMode(empty, "paused")).toBe("loading");
    expect(resolveSourceStrategyMode(empty, "running", "boom")).toBe("error");
    expect(resolveSourceStrategyMode(ready, "running")).toBe("partial");
    expect(resolveSourceStrategyMode(ready, "paused")).toBe("partial");
    expect(resolveSourceStrategyMode(ready, "done")).toBe("ready");
    expect(resolveSourceStrategyMode(ready, "drafting")).toBe("ready");
  });

  it("resolves boundary five-state matrix (LRM-978 / LRM-1282)", () => {
    const empty = { aiCeiling: "", mustHuman: "", matrix: [], empty: true };
    const ready = {
      empty: false,
      aiCeiling: "no licensed advice",
      mustHuman: "compliance review",
      matrix: [] as { human: string; ai: string }[],
    };
    expect(resolveHumanBoundaryMode(empty, "drafting")).toBe("empty");
    expect(resolveHumanBoundaryMode(empty, "running")).toBe("loading");
    expect(resolveHumanBoundaryMode(empty, "paused")).toBe("loading");
    expect(resolveHumanBoundaryMode(empty, "running", "boom")).toBe("error");
    expect(resolveHumanBoundaryMode(ready, "running")).toBe("partial");
    expect(resolveHumanBoundaryMode(ready, "paused")).toBe("partial");
    expect(resolveHumanBoundaryMode(ready, "done")).toBe("ready");
    expect(resolveHumanBoundaryMode(ready, "drafting")).toBe("ready");
  });

  it("splits general vs domain source chips and keeps why", () => {
    const sources: ResearchSource[] = [
      {
        id: "1",
        session_id: "s",
        url: "https://a",
        title: "Docs",
        source_class: "docs",
        credibility_weight: 0.9,
        stance: "",
        relevance: 1,
        summary: "",
        excerpt: "",
        payload: { why: "通用权威基线" },
        created_at: "",
        updated_at: "",
      },
      {
        id: "2",
        session_id: "s",
        url: "https://b",
        title: "SteamDB",
        source_class: "marketplace",
        credibility_weight: 0.7,
        stance: "",
        relevance: 1,
        summary: "",
        excerpt: "",
        payload: { why: "领域供给数据" },
        created_at: "",
        updated_at: "",
      },
    ];
    const model = buildSourceStrategy(sources);
    expect(model.empty).toBe(false);
    expect(model.chips.find((c) => c.label === "docs")?.layer).toBe("general");
    expect(model.chips.find((c) => c.label === "marketplace")?.layer).toBe("domain");
    expect(model.whyLine).toMatch(/通用|领域/);
  });

  it("resolves evidence overview five-state + permission (LRM-1329)", () => {
    const emptySource = { chips: [], whyLine: "", empty: true };
    const readySource = {
      empty: false,
      whyLine: "why",
      chips: [
        {
          id: "docs",
          label: "docs",
          layer: "general" as const,
          samples: [],
        },
      ],
    };
    const emptyBoundary = {
      aiCeiling: "",
      mustHuman: "",
      matrix: [] as { human: string; ai: string }[],
      empty: true,
    };
    const readyBoundary = {
      empty: false,
      aiCeiling: "no licensed advice",
      mustHuman: "compliance review",
      matrix: [] as { human: string; ai: string }[],
    };

    expect(
      resolveEvidenceOverviewMode({
        sourceModel: emptySource,
        boundaryModel: emptyBoundary,
        sessionStatus: "drafting",
      }),
    ).toBe("empty");
    expect(
      resolveEvidenceOverviewMode({
        sourceModel: emptySource,
        boundaryModel: emptyBoundary,
        sessionStatus: "running",
      }),
    ).toBe("loading");
    expect(
      resolveEvidenceOverviewMode({
        sourceModel: readySource,
        boundaryModel: emptyBoundary,
        sessionStatus: "running",
      }),
    ).toBe("partial");
    expect(
      resolveEvidenceOverviewMode({
        sourceModel: readySource,
        boundaryModel: readyBoundary,
        sessionStatus: "running",
      }),
    ).toBe("partial");
    expect(
      resolveEvidenceOverviewMode({
        sourceModel: readySource,
        boundaryModel: readyBoundary,
        sessionStatus: "done",
      }),
    ).toBe("ready");
    expect(
      resolveEvidenceOverviewMode({
        sourceModel: emptySource,
        boundaryModel: emptyBoundary,
        sessionStatus: "running",
        error: "boom",
      }),
    ).toBe("error");
    expect(
      resolveEvidenceOverviewMode({
        sourceModel: readySource,
        boundaryModel: readyBoundary,
        sessionStatus: "done",
        error: "forbidden",
        errorStatus: 403,
      }),
    ).toBe("permission");
    // Revision key changes when facts change — never encodes trust.
    expect(
      evidenceRevisionKey(readySource, readyBoundary),
    ).not.toEqual(evidenceRevisionKey(emptySource, emptyBoundary));
  });

  it("extracts human↔AI boundary from text", () => {
    const model = buildHumanBoundary(
      [
        node({
          id: "b1",
          node_type: "finding",
          title: "人机边界",
          summary: "AI 上限：不能做持牌建议\n必须有人：合规终审\n人做：签署 / AI做：草稿检索",
          payload: { dimension_family: "human_ai_boundary" },
        }),
      ],
      null,
    );
    expect(model.empty).toBe(false);
    expect(model.aiCeiling).toMatch(/持牌/);
    expect(model.mustHuman).toMatch(/合规/);
  });
});
