import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type {
  ResearchV6DirectorNodeDetail,
  ResearchV6DirectorProjectionNode,
} from "@multica/core/types/research-v6-director";
import enResearch from "../../locales/en/research.json";
import { ResearchV6NodeDetail } from "./research-v6-node-detail";

vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      selector: (bundle: typeof enResearch) => unknown,
      values?: Record<string, string | number>,
    ) => {
      const value = selector(enResearch);
      if (typeof value !== "string" || !values) return value;
      return Object.entries(values).reduce(
        (result, [key, replacement]) =>
          result.replace(`{{${key}}}`, String(replacement)),
        value,
      );
    },
  }),
}));

const node = (id: string, title: string): ResearchV6DirectorProjectionNode => ({
  id,
  kind: "insight",
  tier: "L",
  canonicalRef: { kind: "insight", id, revision: 2, contentHash: "sha256:test" },
  branchIds: ["branch-1"],
  state: { execution: "succeeded", conclusion: "accepted", integration: "candidate" },
  title,
  catalogSummary: `${title} summary`,
  absorbed: false,
  terminal: false,
  expandable: true,
  hiddenChildCount: 1,
  updatedAt: "2026-08-17T00:00:00Z",
});

describe("ResearchV6NodeDetail", () => {
  it("shows the assigned Agent, mission, and live execution process for Work S", () => {
    const work = {
      ...node("work-node", "深读手"),
      kind: "work_s",
      tier: "S",
      canonicalRef: { kind: "work_item", id: "work-item-1" },
      state: { execution: "running", conclusion: "proposed", integration: "unmatched" },
      catalogSummary: "核查网页游戏的技术实现与成本。",
    } satisfies ResearchV6DirectorProjectionNode;

    render(
      <ResearchV6NodeDetail
        node={work}
        loading={false}
        error={false}
        selectedForChat={false}
        projectionNodeById={new Map()}
        workActivity={{
          workItemId: "work-item-1",
          attemptId: "attempt-1",
          agentId: "agent-1",
          agentName: "深读手",
          inboxTaskId: "task-1",
          mission: "核查网页游戏的技术实现与成本。",
          status: "running",
          progress: "正在核对浏览器兼容性数据",
          progressStep: 1,
          progressTotal: 3,
          startedAt: "2026-08-17T00:00:00Z",
          updatedAt: "2026-08-17T00:00:00Z",
          timeline: [],
          timelineHasMore: false,
        }}
        workTimeline={[
          {
            id: "activity-1",
            occurred_at: "2026-08-17T00:00:01Z",
            title: "Searching web",
            subtext: "WebGPU compatibility",
            activity_kind: "working",
            detail_kind: "searching_web",
            body_kind: "none",
          },
          {
            id: "activity-2",
            occurred_at: "2026-08-17T00:00:02Z",
            title: "Running command",
            subtext: "检查 Canvas 兼容性数据",
            activity_kind: "working",
            detail_kind: "running_command",
            body_kind: "command",
          },
        ]}
        onRetry={vi.fn()}
        onReference={vi.fn()}
        onFocusNode={vi.fn()}
      />,
    );

    expect(screen.getByRole("region", { name: "Agent work activity" })).toHaveTextContent("深读手");
    expect(screen.getAllByText("核查网页游戏的技术实现与成本。")).toHaveLength(2);
    expect(screen.getByText("Searching web")).toBeTruthy();
    expect(screen.getByText("WebGPU compatibility")).toBeTruthy();
    expect(screen.getByText("Running command")).toBeTruthy();
    expect(screen.getByText("检查 Canvas 兼容性数据")).toBeTruthy();
    expect(screen.getByRole("progressbar", { name: "Latest progress" })).toHaveAttribute(
      "aria-valuenow",
      "33",
    );
  });

  it("shows a retry action when Agent activity cannot be loaded", () => {
    const work = {
      ...node("work-node", "Researcher"),
      kind: "work_s",
      tier: "S",
      canonicalRef: { kind: "work_item", id: "work-item-1" },
    } satisfies ResearchV6DirectorProjectionNode;
    const onRetryWorkActivity = vi.fn();

    render(
      <ResearchV6NodeDetail
        node={work}
        loading={false}
        error={false}
        workActivityError
        selectedForChat={false}
        projectionNodeById={new Map()}
        onRetry={vi.fn()}
        onRetryWorkActivity={onRetryWorkActivity}
        onReference={vi.fn()}
        onFocusNode={vi.fn()}
      />,
    );

    expect(screen.getByRole("alert")).toHaveTextContent(
      "The Agent's work activity could not be loaded.",
    );
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetryWorkActivity).toHaveBeenCalledOnce();
  });

  it("shows what a result node set out to do and what it found", () => {
    const result = {
      ...node("result-node", "Product landscape"),
      kind: "result_s",
      tier: "S",
      canonicalRef: { kind: "result", id: "result-1", revision: 1 },
    } satisfies ResearchV6DirectorProjectionNode;
    const detail = {
      snapshotId: "snapshot-1",
      throughEventSequence: 8,
      projectionHash: "sha256:projection",
      view: "full",
      node: result,
      contentLayers: {
        catalogSummary: "Bounded catalog summary",
        briefSummary: "Brief result summary",
        objective: "Verify which AI employee products are active.",
        conclusion: "Three products have public evidence of active development.",
        content: "The evidence and comparison behind the conclusion.",
        scope: { market: "global" },
        uncertainties: ["Private deployments are not observable."],
        conflicts: [],
        openQuestions: ["Which products publish retention data?"],
      },
      incoming: [],
      outgoing: [],
      historyRefs: [],
      agentRefs: [],
      workItemRefs: [],
      attemptRefs: [],
      evidenceRefs: [],
      discussionRefs: [],
      reportRefs: [],
    } as ResearchV6DirectorNodeDetail & {
      contentLayers: {
        catalogSummary: string;
        briefSummary: string;
        objective: string;
        conclusion: string;
        content: string;
        scope: Record<string, unknown>;
        uncertainties: string[];
        conflicts: string[];
        openQuestions: string[];
      };
    };

    render(
      <ResearchV6NodeDetail
        node={result}
        detail={detail}
        loading={false}
        error={false}
        selectedForChat={false}
        projectionNodeById={new Map()}
        onRetry={vi.fn()}
        onReference={vi.fn()}
        onFocusNode={vi.fn()}
      />,
    );

    expect(screen.getByRole("region", { name: "Result meaning" })).toHaveTextContent(
      "Verify which AI employee products are active.",
    );
    expect(screen.getByRole("region", { name: "Result meaning" })).toHaveTextContent(
      "Three products have public evidence of active development.",
    );
    expect(screen.getByText("What this node investigated")).toBeTruthy();
    expect(screen.getByText("What it found")).toBeTruthy();
  });

  it("locates a server-declared related node and exposes immutable history", () => {
    const current = node("current", "Synthesis");
    const input = node("input", "Supporting result");
    const detail: ResearchV6DirectorNodeDetail = {
      snapshotId: "snapshot-1",
      throughEventSequence: 8,
      projectionHash: "sha256:projection",
      view: "full",
      node: current,
      incoming: [
        {
          id: "edge-1",
          kind: "derived_from",
          fromNodeId: "input",
          toNodeId: "current",
          canonical: true,
          hiddenCount: 0,
          expandable: false,
        },
      ],
      outgoing: [
        {
          id: "edge-future",
          kind: "future_relation_kind",
          fromNodeId: "current",
          toNodeId: "input",
          canonical: false,
          hiddenCount: 0,
          expandable: false,
        },
      ],
      historyRefs: [{ kind: "insight", id: "current", revision: 1 }],
      agentRefs: [{ kind: "agent", id: "agent-1", revision: 3 }],
      workItemRefs: [],
      attemptRefs: [],
      evidenceRefs: [],
      discussionRefs: [],
      reportRefs: [],
    };
    const onFocusNode = vi.fn();

    render(
      <ResearchV6NodeDetail
        node={current}
        detail={detail}
        loading={false}
        error={false}
        selectedForChat={false}
        projectionNodeById={new Map([[input.id, input]])}
        onRetry={vi.fn()}
        onReference={vi.fn()}
        onFocusNode={onFocusNode}
      />,
    );

    fireEvent.click(screen.getAllByRole("button", { name: /Supporting result/i })[0]!);
    expect(onFocusNode).toHaveBeenCalledWith("input");
    expect(screen.getByText("Version history")).toBeTruthy();
    expect(screen.getByText(/Revision 1/)).toBeTruthy();
    expect(screen.getByText("future_relation_kind")).toBeTruthy();
    expect(screen.getByText("Canonical source")).toBeTruthy();
    expect(screen.getByText("Content hash")).toBeTruthy();
    expect(screen.getAllByText("Agents · 1")).toHaveLength(2);
    const projectionState = screen.getByRole("region", { name: "Projection state" });
    expect(projectionState).toHaveTextContent("Absorbed");
    expect(projectionState).toHaveTextContent("Terminal");
    expect(projectionState).toHaveTextContent("Expandable");
    expect(projectionState).toHaveTextContent("Hidden children");
    expect(projectionState).toHaveTextContent("1");
  });
});
