import { describe, expect, it } from "vitest";
import type { ResearchGraphNode, ResearchSource } from "@multica/core/types";
import {
  buildExplorationDimensions,
  buildHumanBoundary,
  buildSourceStrategy,
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

  it("resolves source strip empty vs loading vs ready (LRM-977)", () => {
    const empty = { chips: [], whyLine: "", empty: true };
    expect(resolveSourceStrategyMode(empty, "drafting")).toBe("empty");
    expect(resolveSourceStrategyMode(empty, "running")).toBe("loading");
    expect(resolveSourceStrategyMode(empty, "running", "boom")).toBe("error");
    expect(
      resolveSourceStrategyMode(
        {
          empty: false,
          whyLine: "why",
          chips: [
            {
              id: "docs",
              label: "docs",
              layer: "general",
              samples: [],
            },
          ],
        },
        "running",
      ),
    ).toBe("ready");
  });

  it("resolves boundary empty vs loading vs ready (LRM-978)", () => {
    const empty = { aiCeiling: "", mustHuman: "", matrix: [], empty: true };
    expect(resolveHumanBoundaryMode(empty, "drafting")).toBe("empty");
    expect(resolveHumanBoundaryMode(empty, "running")).toBe("loading");
    expect(resolveHumanBoundaryMode(empty, "running", "boom")).toBe("error");
    expect(
      resolveHumanBoundaryMode(
        {
          empty: false,
          aiCeiling: "no licensed advice",
          mustHuman: "compliance review",
          matrix: [],
        },
        "running",
      ),
    ).toBe("ready");
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
