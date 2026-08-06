// @vitest-environment jsdom

/**
 * LRM-1206 — [巡检][C] no-login static a11y for node-detail evidence empty / association.
 * Companion to LRM-1091 C-area evidence contract (explicit source_id(s) only).
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { ResearchGraphNode, ResearchSource } from "@multica/core/types";
import { ResearchNodeDetail } from "./research-node-detail";

/** Exact structural visibility flips — do not match sm:flex-row / sm:flex-1. */
const FORBIDDEN_STRUCTURAL_SM =
  /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        overlay: { detail_close: "Close detail" },
        panel: { weight: "Weight" },
        node: {
          goal: "Goal",
          subquestion: "Sub",
          probe: "Probe",
          finding: "Finding",
          conflict: "Conflict",
          dead_end: "Dead end",
          refuted: "Refuted",
          pivot: "Pivot",
          roster_change: "Roster",
          stage_gate: "Gate",
          product_round_gate: "Round",
          agent_activity: "Activity",
          confidence: "Confidence",
          detail_hint: "Node detail",
          summary: "Summary",
          doing: "In progress",
          summary_empty: "No summary",
          dead_end_reason: "Why blocked",
          evidence: "Evidence",
          evidence_empty: "No evidence",
          status: {
            active: "Active",
            done: "Done",
            running: "Running",
            waiting: "Waiting",
            abandoned: "Abandoned",
            failed: "Failed",
            completed: "Completed",
            resolved: "Resolved",
            pending: "Pending",
            unknown: "Unknown",
          },
        },
        content_faces: {
          goal: "Goal",
          operation_approach: "Operation approach",
          research_approach: "Research approach",
          result: "Result",
          missing: "Not provided",
          result_pending: "Result in progress",
          result_pending_detail: "Still organizing — no displayable result yet.",
          result_failed: "No displayable result this round",
          result_failed_detail: "No displayable result was produced this round.",
        },
      }),
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@multica/ui/components/ui/sheet", () => ({
  Sheet: ({
    open,
    children,
  }: {
    open?: boolean;
    children?: ReactNode;
  }) => (open ? <div>{children}</div> : null),
  SheetContent: ({
    children,
    ...rest
  }: {
    children?: ReactNode;
    "data-testid"?: string;
    "data-placement"?: string;
  }) => (
    <div
      data-testid={rest["data-testid"] ?? "sheet-content"}
      data-placement={rest["data-placement"]}
    >
      {children}
    </div>
  ),
  SheetHeader: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  SheetTitle: ({ children }: { children?: ReactNode }) => <h2>{children}</h2>,
  SheetDescription: ({ children }: { children?: ReactNode }) => <p>{children}</p>,
}));

function makeNode(
  partial: Partial<ResearchGraphNode> & Pick<ResearchGraphNode, "id" | "title">,
): ResearchGraphNode {
  return {
    session_id: "s1",
    node_type: "finding",
    summary: "Summary body",
    status: "done",
    actor_agent_id: null,
    payload: {},
    created_at: "2026-08-03T00:00:00Z",
    updated_at: "2026-08-03T00:00:00Z",
    ...partial,
  };
}

function makeSource(
  partial: Partial<ResearchSource> & Pick<ResearchSource, "id" | "url" | "title">,
): ResearchSource {
  return {
    credibility_weight: 0.5,
    excerpt: null,
    ...partial,
  } as ResearchSource;
}

const SESSION_SOURCES: ResearchSource[] = [
  makeSource({
    id: "other-a",
    url: "https://docs.example/other-a",
    title: "Session source A (high weight)",
    credibility_weight: 0.99,
    excerpt: "must not appear",
  }),
  makeSource({
    id: "other-b",
    url: "https://docs.example/other-b",
    title: "Session source B",
    credibility_weight: 0.95,
  }),
  makeSource({
    id: "other-c",
    url: "https://docs.example/other-c",
    title: "Session source C",
    credibility_weight: 0.9,
  }),
];

describe("research node-detail evidence a11y static contract (LRM-1206)", () => {
  it("bans sm structural visibility flips on research-node-detail", () => {
    const src = readSrc("research-node-detail.tsx");
    expect(src).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
  });

  it("source: evidenceList filters only source_id / source_ids; no session-wide credibility fallback", () => {
    const src = readSrc("research-node-detail.tsx");
    expect(src).toMatch(/source_id/);
    expect(src).toMatch(/source_ids/);
    expect(src).toMatch(/Never fall/);
    expect(src).toMatch(/back to session-wide sources/);
    // Evidence list must not sort/filter by weight as a substitute for association.
    const evidenceBlock = src.slice(
      src.indexOf("const evidenceList"),
      src.indexOf("return (", src.indexOf("const evidenceList")),
    );
    expect(evidenceBlock).toMatch(/source_ids/);
    expect(evidenceBlock).not.toMatch(/credibility_weight/);
    expect(evidenceBlock).not.toMatch(/\.sort\(/);
  });

  it("empty association: Evidence heading + No evidence; zero source links despite session sources", () => {
    const node = makeNode({
      id: "n-empty",
      title: "Unlinked finding",
      payload: { confidence: 0.4 },
    });
    render(<ResearchNodeDetail node={node} sources={SESSION_SOURCES} open />);

    const detail = screen.getByTestId("research-node-detail");
    expect(detail).toHaveAccessibleName("Node detail");

    expect(
      within(detail).getByRole("heading", { level: 3, name: "Evidence" }),
    ).toBeInTheDocument();
    expect(within(detail).getByText("No evidence")).toBeInTheDocument();

    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.queryByText("Session source A (high weight)")).toBeNull();
    expect(screen.queryByText("Session source B")).toBeNull();
    expect(screen.queryByText("Session source C")).toBeNull();
  });

  it("explicit source_id: only that source is a link; unrelated session sources stay out", () => {
    const node = makeNode({
      id: "n-one",
      title: "Linked finding",
      payload: { source_id: "src1" },
    });
    const sources = [
      makeSource({
        id: "src1",
        url: "https://docs.example/linked",
        title: "Explicitly linked",
        credibility_weight: 0.4,
      }),
      ...SESSION_SOURCES,
    ];
    render(<ResearchNodeDetail node={node} sources={sources} open />);

    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAccessibleName("Explicitly linked");
    expect(links[0]).toHaveAttribute("href", "https://docs.example/linked");
    expect(links[0]).toHaveAttribute("rel", "noreferrer");
    expect(screen.queryByText("Session source A (high weight)")).toBeNull();
    expect(screen.queryByText("No evidence")).toBeNull();
  });

  it("explicit source_ids: only listed sources link; highest-weight unrelated excluded", () => {
    const node = makeNode({
      id: "n-multi",
      title: "Multi-linked",
      payload: { source_ids: ["src2", "src3"] },
    });
    const sources = [
      makeSource({
        id: "src2",
        url: "https://docs.example/b",
        title: "Associated B",
        credibility_weight: 0.2,
      }),
      makeSource({
        id: "src3",
        url: "https://docs.example/c",
        title: "Associated C",
        credibility_weight: 0.3,
      }),
      ...SESSION_SOURCES,
    ];
    render(<ResearchNodeDetail node={node} sources={sources} open />);

    const links = screen.getAllByRole("link");
    expect(links.map((el) => el.textContent)).toEqual([
      "Associated B",
      "Associated C",
    ]);
    expect(screen.queryByText("Session source A (high weight)")).toBeNull();
  });
});
