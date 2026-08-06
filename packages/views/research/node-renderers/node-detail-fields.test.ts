/**
 * LRM-1475 — node-detail-fields: card-face rows are parsed from the canonical
 * Projection node + opaque detail without inventing facts, with spec neutral
 * fallbacks when absent.
 */
import { describe, expect, it } from "vitest";
import { nodeCardFacts } from "./node-detail-fields";

describe("node-detail-fields (card face rows)", () => {
  it("maps owner from canonical actor_agent_id first", () => {
    const f = nodeCardFacts({ actorAgentId: "agent:7", detail: {} });
    expect(f.owner).toBe("agent:7");
  });

  it("falls back to detail assigned_agent_id / agent_id when no canonical actor", () => {
    expect(nodeCardFacts({ actorAgentId: null, detail: { assigned_agent_id: "agent:2" } }).owner).toBe("agent:2");
    expect(nodeCardFacts({ actorAgentId: null, detail: { agent_id: "agent:3" } }).owner).toBe("agent:3");
  });

  it("uses neutral 未分配 / 目标未提供 / 暂无执行动作 fallbacks on absent facts", () => {
    const f = nodeCardFacts({ actorAgentId: null, detail: {} });
    expect(f.owner).toBe("未分配");
    expect(f.objective).toBe("目标未提供");
    expect(f.currentAction).toBe("暂无执行动作");
  });

  it("reads objective from documented aliases", () => {
    expect(nodeCardFacts({ actorAgentId: null, detail: { objective: "核验来源" } }).objective).toBe("核验来源");
    expect(nodeCardFacts({ actorAgentId: null, detail: { question: "为什么失败？" } }).objective).toBe("为什么失败？");
  });

  it("never fills objective with the title (spec §2 rule)", () => {
    const f = nodeCardFacts({ actorAgentId: null, title: "某个标题", detail: {} });
    expect(f.objective).toBe("目标未提供");
  });

  it("reads current action from current_action / action phase", () => {
    expect(nodeCardFacts({ actorAgentId: null, detail: { current_action: "正在核验 3 个来源" } }).currentAction).toBe("正在核验 3 个来源");
  });

  it("parses resolved / progress / risk counts (0 stays 0, blank stays null)", () => {
    const f = nodeCardFacts({
      actorAgentId: null,
      detail: { resolved_count: 2, progress_count: 1, risk_count: 0 },
    });
    expect(f.resolvedCount).toBe(2);
    expect(f.progressCount).toBe(1);
    expect(f.riskCount).toBe(0);
  });

  it("never throws on malformed / non-object detail", () => {
    const f = nodeCardFacts({ actorAgentId: null, detail: "bogus" });
    expect(f.objective).toBe("目标未提供");
    const f2 = nodeCardFacts({ actorAgentId: null, detail: null });
    expect(f2.owner).toBe("未分配");
  });
});
