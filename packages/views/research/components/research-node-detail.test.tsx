import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
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
        actions: { cancel: "Cancel" },
        ring: {
          continue: "Continue research",
          fork: "Fork",
          retry: "Retry",
          reassign: "Reassign",
          reassign_confirm: "Confirm reassign?",
        },
        d5: {
          detail: {
            open_report: "Open report",
            command_pending: "Applying…",
          },
        },
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
          attempt_history: "Execution history",
          contributors: "Contributors",
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
          decision_question: "Decision",
          phase_label: "Phase",
          started_at: "Started",
          updated_at: "Updated",
          duration: "Duration",
          input: "Input",
          gate_blocker: "Gate blocked",
          next_steps: "Next steps",
          next_step_actions: {
            continue: "Continue",
            fork: "Fork",
            retry: "Retry",
            reassign: "Reassign",
          },
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

const node: ResearchGraphNode & {
  content: {
    goal: string;
    operation_approach: string;
    research_approach: string;
    result: string;
  };
} = {
  id: "n1",
  session_id: "s1",
  node_type: "finding",
  title: "价格区间已交叉验证",
  summary: "挂牌中位价与成交价差约 8%。",
  status: "done",
  actor_agent_id: null,
  payload: { confidence: 0.82, source_id: "src1" },
  content: {
    goal: "",
    operation_approach: "",
    research_approach: "",
    result: "交叉验证结果已写入研究节点。",
  },
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
    expect(screen.getByText("交叉验证结果已写入研究节点。")).toBeInTheDocument();
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

  it("wraps unbroken projection text inside narrow detail surfaces", () => {
    render(
      <ResearchNodeDetail
        node={{
          ...node,
          title: `https://example.test/${"segment".repeat(80)}`,
          summary: "散列".repeat(200),
        }}
        sources={sources}
        open
        placement="inline"
      />,
    );

    expect(screen.getByTestId("research-node-detail-body")).toHaveClass(
      "min-w-0",
      "[overflow-wrap:anywhere]",
    );
  });

  it("keeps a pending node command focusable and suppresses reactivation", () => {
    const onNodeCommand = vi.fn();
    render(
      <ResearchNodeDetail
        node={node}
        sources={sources}
        open
        placement="inline"
        onNodeCommand={onNodeCommand}
        pendingNodeCommand="continue"
      />,
    );

    const button = screen.getByRole("button", { name: "Applying…" });
    expect(button).toHaveAttribute("aria-disabled", "true");
    expect(button).not.toBeDisabled();
    button.focus();
    fireEvent.click(button);
    expect(button).toHaveFocus();
    expect(onNodeCommand).not.toHaveBeenCalled();
  });

  it("confirms reassign through an accessible dialog", () => {
    const onNodeCommand = vi.fn();
    render(
      <ResearchNodeDetail
        node={{
          ...node,
          node_type: "task",
          status: "running",
          payload: { task_id: "task-1" },
        }}
        sources={sources}
        open
        placement="inline"
        onNodeCommand={onNodeCommand}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Reassign" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Confirm reassign?");
    expect(onNodeCommand).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("research-node-reassign-confirm"));
    expect(onNodeCommand).toHaveBeenCalledWith("reassign");
  });

  it("shows dead-end reason when node is blocked", () => {
    const dead: ResearchGraphNode = {
      ...node,
      node_type: "dead_end",
      title: "法规路径不通",
      summary: "缺少本地条例全文。",
      payload: { reason: "权威源不可达" },
    };
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
          result: "Pricing evidence now has independent corroboration.",
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
          expected_result: "research_evidence_v3",
          acceptance_criteria: { schema_version: 2, minimum_independent_sources: 2 },
          status: "succeeded",
          assigned_agent_id: "agent-1",
          attempt_count: 2,
          max_attempts: 3,
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
        {
          id: "attempt-2",
          task_id: "task-1",
          attempt_number: 2,
          assigned_agent_id: "agent-2",
          status: "retryable_failed",
          completed_at: "2026-08-04T01:00:00Z",
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
      {
        id: "member-2",
        agent_id: "agent-2",
        role: "validator",
        status: "active",
        is_lead: false,
        display_name: "Validator Lin",
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
    expect(screen.getAllByText("Scout Ada").length).toBeGreaterThan(0);
    expect(screen.getByTestId("node-detail-contributors")).toHaveTextContent(
      "Scout Ada · scout",
    );
    expect(screen.getByTestId("node-detail-contributors")).toHaveTextContent(
      "Validator Lin · validator",
    );
    expect(screen.getByTestId("node-detail-attempt-history")).toHaveTextContent(
      "Attempt 1",
    );
    expect(screen.getByTestId("node-detail-attempt-history")).toHaveTextContent(
      "Attempt 2",
    );
    expect(screen.getByTestId("node-detail-attempt-history")).toHaveTextContent(
      "Failed",
    );
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

  it("LRM-1410 residual: header renders real decision question, phase and run timestamps", () => {
    const runNode: ResearchGraphNode = {
      ...node,
      node_type: "probe",
      status: "running",
      phase: "s2_sources",
      payload: { phase: "s2_sources" },
    };
    const run = {
      run: { current_stage: "s2_sources", last_progress_at: "2026-08-05T11:00:00Z" },
      method: {
        decision_question: "What drives the listing-to-transaction price gap?",
      },
      contract: { goal: "Should never be used when method present" },
      tasks: [
        {
          id: "task-1",
          client_key: "gap",
          kind: "probe",
          objective: "Probe the price gap driver.",
          required_capability: "scout",
          status: "in_flight",
          assigned_agent_id: "agent-1",
          attempt_count: 1,
          goal_version: 1,
          plan_version: 1,
          started_at: "2026-08-05T10:30:00Z",
          completed_at: "2026-08-05T10:42:00Z",
        },
      ],
      attempts: [],
      sources: [],
      observations: [],
      claims: [],
      questions: [],
    } as unknown as ResearchRunSnapshot;
    const runNodeWithIds = {
      ...runNode,
      payload: {
        details: { task_id: "task-1", attempt_id: "attempt-1", phase: "s2_sources" },
      },
    };

    render(<ResearchNodeDetail node={runNodeWithIds} sources={[]} run={run} open />);

    expect(screen.getByTestId("node-detail-decision-question")).toHaveTextContent(
      "What drives the listing-to-transaction price gap?",
    );
    expect(screen.getByTestId("node-detail-phase")).toHaveTextContent("s2_sources");
    expect(screen.getByTestId("node-detail-started")).toBeInTheDocument();
    // duration from real task started→completed
    expect(screen.getByTestId("node-detail-duration")).toHaveTextContent("12m");
  });

  it("LRM-1410 residual: renders explicit Input block from task acceptance criteria", () => {
    const inputNode: ResearchGraphNode = {
      ...node,
      payload: {
        details: {
          task_id: "task-1",
          attempt_id: "attempt-1",
        },
      },
    };
    const run = {
      tasks: [
        {
          id: "task-1",
          client_key: "market-prices",
          kind: "discover",
          objective: "Collect prices.",
          required_capability: "scout",
          acceptance_criteria: {
            schema_version: 2,
            minimum_independent_sources: 2,
          },
          status: "succeeded",
          attempt_count: 1,
          goal_version: 1,
          plan_version: 1,
          started_at: "2026-08-05T09:00:00Z",
        },
      ],
      attempts: [],
      sources: [],
      observations: [],
      claims: [],
      questions: [],
    } as unknown as ResearchRunSnapshot;

    render(<ResearchNodeDetail node={inputNode} sources={[]} run={run} open />);

    const inputBlock = screen.getByTestId("node-detail-input");
    expect(inputBlock).toHaveTextContent("Input");
    expect(inputBlock).toHaveTextContent("minimum_independent_sources");
    expect(inputBlock).toHaveTextContent("2");
  });

  it("LRM-1410 residual: stage_gate node surfaces real run gate findings with severity", () => {
    const gateNode: ResearchGraphNode = {
      ...node,
      node_type: "stage_gate",
      status: "waiting",
      payload: {},
    };
    const run = {
      gate: {
        passed: false,
        findings: [
          {
            code: "GATE-1",
            severity: "error",
            message: "Independent corroboration for the price claim is missing.",
          },
          {
            code: "GATE-2",
            severity: "warning",
            message: "Coverage gap on transaction registry remains.",
          },
        ],
      },
      tasks: [],
      attempts: [],
      sources: [],
      observations: [],
      claims: [],
      questions: [],
    } as unknown as ResearchRunSnapshot;

    render(<ResearchNodeDetail node={gateNode} sources={[]} run={run} open />);

    const block = screen.getByTestId("node-detail-gate-blocker");
    expect(block).toHaveTextContent("Gate blocked");
    expect(
      block,
    ).toHaveTextContent(
      "Independent corroboration for the price claim is missing.",
    );
    expect(block).toHaveTextContent("Coverage gap on transaction registry remains.");
    expect(block).toHaveTextContent("error");
  });

  it("LRM-1410 residual: done node shows status-aware read-only next steps", () => {
    const doneNode: ResearchGraphNode = {
      ...node,
      status: "done",
      payload: {},
    };
    render(<ResearchNodeDetail node={doneNode} sources={[]} open />);
    expect(screen.getByTestId("node-detail-next-steps")).toBeInTheDocument();
    expect(screen.getByTestId("node-detail-next-step-continue")).toHaveTextContent("Continue");
    expect(screen.getByTestId("node-detail-next-step-fork")).toHaveTextContent("Fork");
  });

  it("LRM-1410 residual: gate blocker and input are absent on a plain finding node", () => {
    render(<ResearchNodeDetail node={node} sources={sources} open />);
    expect(screen.queryByTestId("node-detail-gate-blocker")).toBeNull();
    expect(screen.queryByTestId("node-detail-input")).toBeNull();
  });
});
