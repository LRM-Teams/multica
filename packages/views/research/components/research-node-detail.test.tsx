import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type {
  ResearchFleetMember,
  ResearchGraphNode,
  ResearchRunSnapshot,
  ResearchSource,
} from "@multica/core/types";
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
          abandon_reason: "Abandon reason",
          abandon_reason_pending: "Reason pending sync",
          evidence: "Evidence",
          evidence_empty: "No evidence",
          objective: "Objective",
          method: "Method",
          executor: "Executor",
          activity: "Actions completed",
          outcome: "Outcome",
          task_type: "Task type",
          attempt: "Attempt",
          executor_role: "Actual role:",
          required_role: "Required role:",
          expected_result: "Expected output:",
          diagnostics: "Diagnostics",
          artifacts: "Artifacts",
          artifact_source: "Source",
          artifact_observation: "Evidence extract",
          artifact_claim: "Research claim",
          artifact_task: "Derived task",
          artifact_question: "New question",
          source_count: "Sources",
          observation_count: "Observations",
          claim_count: "Claims",
          task_count: "New tasks",
          question_count: "New questions",
          report_created: "Report produced",
          task_kinds: {
            discover: "Discover and select sources",
          },
          expected_results: {
            evidence: "Reviewable evidence package",
          },
          task_methods: {
            discover: "Search candidate sources and create evidence snapshots",
          },
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

  it("LRM-1333: abandoned detail shows abandon_reason; never reason/dead_end_reason", () => {
    const abandoned: ResearchGraphNode = {
      ...node,
      status: "abandoned",
      title: "定价支",
      assessment: "detour",
      reason: "弯路质量原因",
      abandon_reason: "用户改成监管合规",
      payload: { reason: "质量原因", dead_end_reason: "死胡同" },
    };
    render(<ResearchNodeDetail node={abandoned} sources={[]} open />);
    const block = screen.getByTestId("research-node-abandon-reason");
    expect(block).toHaveTextContent("Abandon reason");
    expect(block).toHaveTextContent("用户改成监管合规");
    expect(block).not.toHaveTextContent("弯路质量原因");
    expect(block).not.toHaveTextContent("质量原因");
    expect(block).not.toHaveTextContent("死胡同");
  });

  it("LRM-1333: abandoned without reason shows pending sync copy", () => {
    const abandoned: ResearchGraphNode = {
      ...node,
      status: "abandoned",
      payload: { reason: "不得回退" },
    };
    render(<ResearchNodeDetail node={abandoned} sources={[]} open />);
    expect(screen.getByTestId("research-node-abandon-reason")).toHaveTextContent(
      "Reason pending sync",
    );
    expect(screen.queryByText("不得回退")).toBeNull();
  });

  it("LRM-1333: non-abandoned nodes omit abandon reason block", () => {
    const detour: ResearchGraphNode = {
      ...node,
      status: "done",
      assessment: "detour",
      abandon_reason: "不应展示",
    };
    render(<ResearchNodeDetail node={detour} sources={[]} open />);
    expect(screen.queryByTestId("research-node-abandon-reason")).toBeNull();
  });

  it("does not fall back to session sources when node has no source association", () => {
    const unlinked: ResearchGraphNode = {
      ...node,
      payload: { confidence: 0.5 },
    };
    const sessionSources: ResearchSource[] = [
      {
        id: "other-a",
        url: "https://docs.example/other-a",
        title: "他人节点来源 A",
        credibility_weight: 0.99,
        excerpt: "不应出现",
      } as ResearchSource,
      {
        id: "other-b",
        url: "https://docs.example/other-b",
        title: "他人节点来源 B",
        credibility_weight: 0.95,
        excerpt: "不应出现",
      } as ResearchSource,
      {
        id: "other-c",
        url: "https://docs.example/other-c",
        title: "他人节点来源 C",
        credibility_weight: 0.9,
        excerpt: "不应出现",
      } as ResearchSource,
    ];
    render(<ResearchNodeDetail node={unlinked} sources={sessionSources} open />);
    expect(screen.getByText("No evidence")).toBeInTheDocument();
    expect(screen.queryByText("他人节点来源 A")).toBeNull();
    expect(screen.queryByText("他人节点来源 B")).toBeNull();
    expect(screen.queryByText("他人节点来源 C")).toBeNull();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("still shows explicitly linked sources via source_id / source_ids", () => {
    const multi: ResearchGraphNode = {
      ...node,
      payload: { source_ids: ["src2", "src3"] },
    };
    const sessionSources: ResearchSource[] = [
      {
        id: "src2",
        url: "https://docs.example/b",
        title: "关联来源 B",
        credibility_weight: 0.7,
      } as ResearchSource,
      {
        id: "src3",
        url: "https://docs.example/c",
        title: "关联来源 C",
        credibility_weight: 0.6,
      } as ResearchSource,
      {
        id: "unrelated",
        url: "https://docs.example/x",
        title: "无关来源",
        credibility_weight: 0.99,
      } as ResearchSource,
    ];
    render(<ResearchNodeDetail node={multi} sources={sessionSources} open />);
    expect(screen.getByText("关联来源 B")).toBeInTheDocument();
    expect(screen.getByText("关联来源 C")).toBeInTheDocument();
    expect(screen.queryByText("无关来源")).toBeNull();
  });

  it("explains a run node's objective, method, executor, actions, artifacts, and outcome", () => {
    const taskNode: ResearchGraphNode = {
      ...node,
      id: "event-node",
      node_type: "finding",
      title: "Research result accepted",
      summary: "Pricing evidence now has independent corroboration.",
      actor_agent_id: null,
      payload: {
        event_type: "task_result_accepted",
        details: {
          task_id: "task-1",
          attempt_id: "attempt-1",
          agent_id: "agent-1",
          task_kind: "discover",
          sources_created: 2,
          observations_created: 3,
          claims_created: 1,
          tasks_created: 4,
          questions_created: 2,
          report_id: "report-1",
        },
      },
    };
    const run = {
      tasks: [
        {
          id: "task-1",
          client_key: "market-prices",
          kind: "discover",
          objective: "Collect two independent primary sources for transaction prices.",
          required_capability: "scout",
          expected_result: "research_evidence_v2",
          acceptance_criteria: { schema_version: 2, minimum_independent_sources: 2 },
          status: "succeeded",
          assigned_agent_id: "agent-1",
          attempt_count: 1,
          goal_version: 1,
          plan_version: 1,
        },
        {
          id: "task-2",
          parent_task_id: "task-1",
          client_key: "follow-up",
          kind: "verify",
          objective: "Verify the price range against the public registry.",
          required_capability: "validator",
          status: "ready",
          attempt_count: 0,
          goal_version: 1,
          plan_version: 1,
        },
      ],
      attempts: [
        {
          id: "attempt-1",
          task_id: "task-1",
          attempt_number: 1,
          assigned_agent_id: "agent-1",
          status: "succeeded",
        },
      ],
      sources: [
        {
          id: "snapshot-1",
          produced_by_task_id: "task-1",
          canonical_url: "https://primary.example/prices",
          title: "Primary transaction register",
          publisher: "Registry",
          source_class: "primary",
          independence_key: "registry",
          retrieved_at: "2026-08-04T00:00:00Z",
          content_hash: "hash",
          snapshot_excerpt: "Median transaction price was 920,000.",
          metadata: {},
          verification_status: "verified",
          created_at: "2026-08-04T00:00:00Z",
        },
      ],
      observations: [
        {
          id: "observation-1",
          source_snapshot_id: "snapshot-1",
          produced_by_task_id: "task-1",
          quote: "Registry row 8 reports a 920,000 median transaction price.",
          datum: { currency: "CNY", value: 920000 },
          locator: "table 3, row 8",
          verification_status: "verified",
          created_at: "2026-08-04T00:00:00Z",
        },
      ],
      claims: [
        {
          id: "claim-1",
          produced_by_task_id: "task-1",
          client_key: "median-price",
          text: "The median transaction price is 920,000.",
          significance: "high",
          confidence: 0.86,
          status: "supported",
          goal_version: 1,
          plan_version: 1,
          evidence: [],
          created_at: "2026-08-04T00:00:00Z",
          updated_at: "2026-08-04T00:00:00Z",
        },
      ],
      questions: [
        {
          id: "question-1",
          created_by_task_id: "task-1",
          client_key: "price-gap",
          kind: "follow_up",
          question: "Why does the listing-to-transaction price gap persist?",
          required: true,
          status: "open",
          priority: 0.8,
          coverage: 0,
          goal_version: 1,
          plan_version: 1,
        },
      ],
    } as unknown as ResearchRunSnapshot;
    const members: ResearchFleetMember[] = [
      {
        id: "member-1",
        agent_id: "agent-1",
        role: "scout",
        status: "active",
        is_lead: false,
        display_name: "Scout Ada",
      },
    ];

    render(
      <ResearchNodeDetail
        node={taskNode}
        sources={[]}
        run={run}
        members={members}
        open
      />,
    );

    expect(screen.getByText("Objective")).toBeInTheDocument();
    expect(
      screen.getByText("Collect two independent primary sources for transaction prices."),
    ).toBeInTheDocument();
    expect(screen.getByText("Method")).toBeInTheDocument();
    expect(
      screen.getByText("Search candidate sources and create evidence snapshots"),
    ).toBeInTheDocument();
    expect(screen.getByText("Scout Ada")).toBeInTheDocument();
    expect(screen.getByText("Discover and select sources")).toBeInTheDocument();
    expect(screen.getByText("Required role:")).toBeInTheDocument();
    expect(screen.getByText("Reviewable evidence package")).toBeInTheDocument();
    expect(screen.getByText("Completed")).toBeInTheDocument();
    expect(screen.getByText("Sources 2")).toBeInTheDocument();
    expect(screen.getByText("Observations 3")).toBeInTheDocument();
    expect(screen.getByText("Claims 1")).toBeInTheDocument();
    expect(screen.getByText("New tasks 4")).toBeInTheDocument();
    expect(screen.getByText("New questions 2")).toBeInTheDocument();
    expect(screen.getByText("Report produced")).toBeInTheDocument();
    expect(screen.getByText("Primary transaction register")).toBeInTheDocument();
    expect(
      screen.getByText("Registry row 8 reports a 920,000 median transaction price."),
    ).toBeInTheDocument();
    expect(screen.getByText("The median transaction price is 920,000.")).toBeInTheDocument();
    expect(
      screen.getByText("Verify the price range against the public registry."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Why does the listing-to-transaction price gap persist?"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Pricing evidence now has independent corroboration."),
    ).toBeInTheDocument();
  });
});
