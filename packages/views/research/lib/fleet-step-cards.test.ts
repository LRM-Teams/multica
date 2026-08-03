// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchMessage } from "@multica/core/types";
import {
  buildFleetChatFeed,
  nextStageWaitingCard,
  presenceRunningCards,
  shortProcessLine,
} from "./fleet-step-cards";

function msg(
  partial: Partial<ResearchMessage> & Pick<ResearchMessage, "id" | "body">,
): ResearchMessage {
  return {
    session_id: "s1",
    sender_type: "system",
    sender_id: null,
    target_agent_id: null,
    card_kind: "process",
    meta: {},
    created_at: "2026-07-31T09:00:00Z",
    ...partial,
  };
}

describe("shortProcessLine", () => {
  it("strips enqueue/snapshot dumps", () => {
    const line = shortProcessLine(
      "未能唤醒目标 agent：enqueue research wake: snapshot agent model is required 请重试",
    );
    expect(line.toLowerCase()).not.toContain("enqueue");
    expect(line.length).toBeLessThan(120);
  });
});

describe("buildFleetChatFeed", () => {
  it("keeps chat bubbles and maps process ops to done cards", () => {
    const feed = buildFleetChatFeed([
      msg({
        id: "u1",
        sender_type: "user",
        card_kind: "chat",
        body: "纠正方向",
        meta: {},
      }),
      msg({
        id: "p1",
        body: "「页游」开题：5 名成员已上画布",
        meta: {
          op: "session_kickoff",
          title: "调研团已就位",
          member_count: 5,
          fine_domain: "game",
          stage: "s1_plan",
        },
      }),
    ]);
    expect(feed).toHaveLength(2);
    expect(feed[0]?.kind).toBe("chat");
    expect(feed[1]?.kind).toBe("step");
    if (feed[1]?.kind === "step") {
      expect(feed[1].status).toBe("done");
      expect(feed[1].title).toBe("调研团已就位");
      expect(feed[1].bullets.some((b) => b.includes("5"))).toBe(true);
    }
  });

  it("merges similar wake_failed into one failed card with count", () => {
    const feed = buildFleetChatFeed([
      msg({
        id: "e1",
        body: "未能唤醒… enqueue research wake: agent model is required",
        meta: {
          op: "wake_failed",
          title: "唤醒失败",
          reason: "agent_model_required",
          recovery_hint: "请配置 model 后重试",
          actor_agent_id: "agent-lead",
        },
      }),
      msg({
        id: "e2",
        body: "未能唤醒… agent model is required",
        meta: {
          op: "wake_failed",
          title: "唤醒失败",
          reason: "agent_model_required",
          actor_agent_id: "agent-lead",
        },
      }),
      msg({
        id: "e3",
        body: "未能唤醒… agent model is required again",
        meta: {
          op: "wake_failed",
          title: "唤醒失败",
          reason: "agent_model_required",
          actor_agent_id: "agent-lead",
        },
      }),
    ]);
    expect(feed).toHaveLength(1);
    expect(feed[0]?.kind).toBe("step");
    if (feed[0]?.kind === "step") {
      expect(feed[0].status).toBe("failed");
      expect(feed[0].mergeCount).toBe(3);
      expect(feed[0].summaryDetail).toContain("3");
      expect(feed[0].showRetry).toBe(true);
      expect(feed[0].showReassign).toBe(true);
      expect(feed[0].summaryHeadline.toLowerCase()).not.toContain("enqueue");
    }
  });

  it("does not merge wake_failed with different reasons", () => {
    const feed = buildFleetChatFeed([
      msg({
        id: "a",
        body: "runtime offline",
        meta: { op: "wake_failed", reason: "runtime_offline", title: "唤醒失败" },
      }),
      msg({
        id: "b",
        body: "archived",
        meta: { op: "wake_failed", reason: "agent_archived", title: "唤醒失败" },
      }),
    ]);
    expect(feed).toHaveLength(2);
  });
});


  it("keeps product_round_judgment as interactive chat cards", () => {
    const feed = buildFleetChatFeed([
      msg({
        id: "r1",
        body: "产品轮判定",
        meta: { op: "product_round_judgment", decision: "continue", title: "判定" },
      }),
    ]);
    expect(feed).toHaveLength(1);
    expect(feed[0]?.kind).toBe("chat");
  });

  it("keeps clarification_question as interactive chat cards (LRM-822)", () => {
    const feed = buildFleetChatFeed([
      msg({
        id: "c1",
        body: "澄清提问",
        meta: {
          op: "clarification_question",
          question_id: "q1",
          layout: "list",
          options: [{ id: "a", label: "A" }],
        },
      }),
    ]);
    expect(feed).toHaveLength(1);
    expect(feed[0]?.kind).toBe("chat");
  });

describe("presenceRunningCards / nextStageWaitingCard", () => {
  it("builds running cards from presence", () => {
    const cards = presenceRunningCards(
      { "agent-1": { activity: "读 4/12" } },
      [
        {
          id: "m1",
          agent_id: "agent-1",
          role: "reader",
          status: "active",
          is_lead: false,
          name: "深读手",
          display_name: "深读手",
        },
      ],
    );
    expect(cards).toHaveLength(1);
    expect(cards[0]?.status).toBe("running");
    expect(cards[0]?.summaryHeadline).toBe("读 4/12");
  });

  it("exposes a waiting card for the next stage", () => {
    const card = nextStageWaitingCard("s2_sources", "running");
    expect(card?.status).toBe("waiting");
    expect(card?.stepLabel).toBe("s3_validation");
  });
});
