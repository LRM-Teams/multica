import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { buildDisputeModel } from "./model";
import { DISPUTE_EDGES, DISPUTE_NODES } from "./__fixtures__/dispute-contract.fixture";
import { DisputeCard } from "./panels";
import { PositionFan, EvidenceRelation } from "./parts";
import { stanceGlyph } from "./stance";

type Dict = Record<string, unknown>;
function dict() {
  return {
    dispute: {
      status: {
        open: "未解决",
        investigating: "调查中",
        deadlocked: "僵局",
        escalated: "升级中",
        resolved: "已裁决",
        conditionally_resolved: "条件裁决",
        irreducible: "不可判定",
        reopened: "已重开",
        cancelled: "已取消",
        converged: "已收敛",
        unresolved: "未收敛",
        pending: "待定",
        discussing: "讨论中",
        current: "当前",
        superseded: "已取代",
      },
      stance: { supports: "支持", contradicts: "反驳", conditional: "条件细化" },
      turn_marker: {
        position_changed: "立场变化",
        evidence_added: "新增证据",
        scope_refined: "范围细化",
        no_change: "无进展",
      },
      conflict_type: { measurement: "口径" },
      gate_blocking: "此争议阻断交付",
      residual_note: "残余影响",
      escalated_to: "升级至",
      director_adjudicating: "研究总监裁决中",
      superseded: "已被新裁决取代",
      view_history: "查看历史",
      positions: "立场",
      evidence: "证据",
      turns: "轮次",
      verdict: "裁决",
      empty: { positions: "尚无立场", evidence: "尚无证据", turns: "尚无轮次", root: "尚无争议" },
      overflow_more: "+{{count}}",
      focus: { node: "在图面定位此节点" },
      conditions_label: "条件",
      conflict_type_label: "冲突类型",
      stance_label: "立场",
      severity_label: "严重度",
    },
    node: {
      dispute: "争议",
      dispute_position: "立场",
      deliberation: "讨论",
      deliberation_turn: "轮次",
      decision: "裁决",
      executor: "执行者",
    },
  };
}

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (d: Dict) => unknown, opts?: { count?: number }) => {
      const raw = String(fn(dict() as never) ?? "");
      return raw.replace(/\{\{count\}\}/g, String(opts?.count ?? ""));
    },
  }),
}));

const model = buildDisputeModel(DISPUTE_NODES, DISPUTE_EDGES);

describe("DisputeCard browse (acceptance 1)", () => {
  it("composes the full fixture: 3 positions, multi-turn, escalation, decision+reopen", () => {
    render(<DisputeCard model={model} />);
    expect(screen.getByTestId("dispute-card")).toBeTruthy();
    // 3 positions
    const rows = screen.getAllByTestId("dispute-position-row");
    expect(rows).toHaveLength(3);
    // multi-turn
    expect(screen.getAllByTestId("dispute-turn-row").length).toBeGreaterThanOrEqual(3);
    // escalation
    expect(screen.getByTestId("dispute-escalation-banner").dataset.escalated).toBe("true");
    // decision current + history retained
    expect(screen.getByTestId("dispute-decision-current")).toBeTruthy();
    expect(screen.getAllByTestId("dispute-decision-history-row").length).toBeGreaterThanOrEqual(1);
  });

  it("non-color encoding: position glyphs distinct between supports and contradicts", () => {
    expect(stanceGlyph("supports")).not.toBe(stanceGlyph("contradicts"));
    expect(stanceGlyph("contradicts")).not.toBe(stanceGlyph("conditional"));
  });

  it("renders the delivery-blocking gate banner for an open dispute", () => {
    render(<DisputeCard model={model} />);
    expect(screen.getByTestId("dispute-blocking-banner").textContent).toContain("阻断交付");
  });
});

describe("DisputeCard focus affordance (acceptance 2)", () => {
  it("P1 focuses a position node via the focus button", () => {
    const onFocusNode = vi.fn();
    render(<DisputeCard model={model} onFocusNode={onFocusNode} />);
    const buttons = screen.getAllByRole("button", { name: /在图面定位此节点/i });
    expect(buttons.length).toBeGreaterThanOrEqual(3);
    fireEvent.click(buttons[0]!);
    expect(onFocusNode).toHaveBeenCalledWith(expect.any(String));
  });
});

describe("PositionFan empty / overflow", () => {
  it("shows a real empty state without fabricated positions", () => {
    render(<PositionFan positions={[]} overflow={[]} />);
    expect(screen.getByText("尚无立场")).toBeTruthy();
    expect(screen.queryByTestId("dispute-position-row")).toBeNull();
  });

  it("collapses overflow positions into a +N chip", () => {
    const overflow = model.positions.slice(0, 1);
    render(<PositionFan positions={model.positions} overflow={overflow} />);
    expect(screen.getByText("+1")).toBeTruthy();
  });
});

describe("EvidenceRelation non-color + empty", () => {
  it("renders supports/contradicts evidence rows", () => {
    render(<EvidenceRelation evidence={model.evidence} />);
    expect(screen.getAllByTestId("dispute-evidence-row").length).toBeGreaterThanOrEqual(3);
  });
});
