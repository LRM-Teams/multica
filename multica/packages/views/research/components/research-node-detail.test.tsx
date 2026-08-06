import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ResearchGraphNode, ResearchSource } from "@multica/core/types";
import type { ReactNode } from "react";
import { ResearchNodeDetail } from "./research-node-detail";

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
          detail_hint: "Detail",
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

const node: ResearchGraphNode = {
  id: "n1",
  session_id: "s1",
  node_type: "finding",
  title: "价格区间已交叉验证",
  summary: "挂牌中位价与成交价差约 8%。",
  status: "done",
  actor_agent_id: null,
  payload: { confidence: 0.82, source_id: "src1" },
  created_at: "2026-07-06T09:00:00Z",
  updated_at: "2026-07-06T09:00:00Z",
};

const sources: ResearchSource[] = [
  {
    id: "src1",
    url: "https://docs.example/a",
    title: "成交样本",
    credibility_weight: 0.88,
    excerpt: "样本 n=120",
  } as ResearchSource,
];

describe("ResearchNodeDetail (LRM-797 / LRM-826)", () => {
  it("desktop default is a substantial overlay card, not a corner float chip", () => {
    render(<ResearchNodeDetail node={node} sources={sources} open />);
    const el = screen.getByTestId("research-node-detail");
    expect(el).toBeInTheDocument();
    expect(el).toHaveAttribute("data-placement", "overlay-card");
    expect(screen.getByText("价格区间已交叉验证")).toBeInTheDocument();
    expect(screen.getByText("挂牌中位价与成交价差约 8%。")).toBeInTheDocument();
    expect(screen.getByText("成交样本")).toBeInTheDocument();
    expect(screen.getByText("Done")).toBeInTheDocument();
    expect(screen.queryByText("done")).toBeNull();
    expect(document.querySelector(".absolute.bottom-4.left-4")).toBeNull();
  });

  it("narrow placement uses bottom sheet", () => {
    render(<ResearchNodeDetail node={node} sources={sources} open placement="sheet" />);
    expect(screen.getByTestId("research-node-detail")).toHaveAttribute(
      "data-placement",
      "sheet",
    );
  });

  it("shows dead-end reason when node is blocked", () => {
    const dead: ResearchGraphNode = {
      ...node,
      node_type: "dead_end",
      title: "法规路径不通",
      summary: "缺少本地条例全文。",
      payload: { reason: "权威源不可达" },
    } as ResearchGraphNode;
    render(<ResearchNodeDetail node={dead} sources={[]} open />);
    expect(screen.getByText("Why blocked")).toBeInTheDocument();
    expect(screen.getByText("权威源不可达")).toBeInTheDocument();
  });
});
