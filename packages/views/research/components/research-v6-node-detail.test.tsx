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
  canonical_ref: { kind: "insight", id, revision: 2, content_hash: "sha256:test" },
  branch_ids: ["branch-1"],
  state: { execution: "succeeded", conclusion: "accepted", integration: "candidate" },
  title,
  catalog_summary: `${title} summary`,
  absorbed: false,
  terminal: false,
  expandable: true,
  hidden_child_count: 1,
  updated_at: "2026-08-17T00:00:00Z",
});

describe("ResearchV6NodeDetail", () => {
  it("shows the assigned Agent, mission, and live execution process for Work S", () => {
    const work = {
      ...node("work-node", "深读手"),
      kind: "work_s",
      tier: "S",
      canonical_ref: { kind: "work_item", id: "work-item-1" },
      state: { execution: "running", conclusion: "proposed", integration: "unmatched" },
      catalog_summary: "核查网页游戏的技术实现与成本。",
    } satisfies ResearchV6DirectorProjectionNode;

    render(
      <ResearchV6NodeDetail
        node={work}
        loading={false}
        error={false}
        selectedForChat={false}
        projectionNodeById={new Map()}
        workActivity={{
          work_item_id: "work-item-1",
          attempt_id: "attempt-1",
          agent_id: "agent-1",
          agent_name: "深读手",
          inbox_task_id: "task-1",
          mission: "核查网页游戏的技术实现与成本。",
          status: "running",
          progress: "正在核对浏览器兼容性数据",
          progress_step: 1,
          progress_total: 3,
          started_at: "2026-08-17T00:00:00Z",
          updated_at: "2026-08-17T00:00:00Z",
          timeline: [],
          timeline_has_more: false,
        }}
        workTimeline={[
          {
            id: "activity-1",
            occurred_at: "2026-08-17T00:00:01Z",
            title: "Searching web",
            subtext: "WebGPU compatibility",
            tone: "warning",
            body_kind: "none",
          },
          {
            id: "activity-2",
            occurred_at: "2026-08-17T00:00:02Z",
            title: "Running command",
            subtext: "检查 Canvas 兼容性数据",
            tone: "running",
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
      canonical_ref: { kind: "work_item", id: "work-item-1" },
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

  it("locates a server-declared related node and exposes immutable history", () => {
    const current = node("current", "Synthesis");
    const input = node("input", "Supporting result");
    const detail: ResearchV6DirectorNodeDetail = {
      snapshot_id: "snapshot-1",
      through_event_sequence: 8,
      projection_hash: "sha256:projection",
      view: "full",
      node: current,
      incoming: [
        {
          id: "edge-1",
          kind: "derived_from",
          from_node_id: "input",
          to_node_id: "current",
          canonical: true,
          hidden_count: 0,
          expandable: false,
        },
      ],
      outgoing: [
        {
          id: "edge-future",
          kind: "future_relation_kind",
          from_node_id: "current",
          to_node_id: "input",
          canonical: false,
          hidden_count: 0,
          expandable: false,
        },
      ],
      history_refs: [{ kind: "insight", id: "current", revision: 1 }],
      agent_refs: [{ kind: "agent", id: "agent-1", revision: 3 }],
      work_item_refs: [],
      attempt_refs: [],
      evidence_refs: [],
      discussion_refs: [],
      report_refs: [],
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
