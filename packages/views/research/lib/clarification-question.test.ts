import { describe, expect, it } from "vitest";
import type { ResearchMessage } from "@multica/core/types";
import {
  formatClarificationFormReply,
  formatClarificationOptionReply,
  formatClarificationSkipReply,
  parseClarificationQuestion,
  resolveClarificationResolution,
} from "./clarification-question";

function msg(partial: Partial<ResearchMessage> & Pick<ResearchMessage, "id" | "body">): ResearchMessage {
  return {
    session_id: "s1",
    sender_type: "agent",
    sender_id: "a1",
    target_agent_id: null,
    card_kind: "process",
    meta: {},
    created_at: "2026-08-02T10:00:00Z",
    ...partial,
  };
}

const listMeta = {
  op: "clarification_question",
  question_id: "q1",
  prompt: "Pick a focus",
  layout: "list",
  allow_skip: true,
  options: [
    { id: "a", label: "Cost" },
    { id: "b", label: "Recall" },
  ],
};

describe("parseClarificationQuestion", () => {
  it("parses list layout from meta", () => {
    const q = parseClarificationQuestion(
      msg({ id: "m1", body: "Pick a focus", meta: listMeta }),
    );
    expect(q).not.toBeNull();
    expect(q?.layout).toBe("list");
    expect(q?.options).toHaveLength(2);
    expect(q?.allow_skip).toBe(true);
  });

  it("parses form layout and ignores freehand-only payloads", () => {
    const q = parseClarificationQuestion(
      msg({
        id: "m2",
        body: "Constraints?",
        meta: {
          op: "clarification_question",
          question_id: "q2",
          prompt: "Constraints?",
          layout: "form",
          fields: [
            { id: "budget", label: "Budget", type: "text", required: true },
            { id: "notes", label: "Notes", type: "textarea" },
          ],
        },
      }),
    );
    expect(q?.layout).toBe("form");
    expect(q?.fields).toHaveLength(2);
    expect(q?.options).toHaveLength(0);
  });

  it("returns null for non-clarification ops", () => {
    expect(
      parseClarificationQuestion(
        msg({ id: "m3", body: "x", meta: { op: "wake_failed" } }),
      ),
    ).toBeNull();
  });

  it("infers binary when two options and layout omitted", () => {
    const q = parseClarificationQuestion(
      msg({
        id: "m4",
        body: "Yes or no?",
        meta: {
          op: "clarification_question",
          question_id: "q4",
          options: [
            { id: "y", label: "Yes" },
            { id: "n", label: "No" },
          ],
        },
      }),
    );
    expect(q?.layout).toBe("binary");
  });
});

describe("format + resolve clarification replies", () => {
  const question = parseClarificationQuestion(
    msg({ id: "ask", body: "Pick a focus", meta: listMeta, created_at: "2026-08-02T10:00:00Z" }),
  )!;

  it("formats option / skip / form bodies with qid tokens", () => {
    expect(formatClarificationOptionReply(question, question.options[0]!)).toContain(
      "[qid=q1]",
    );
    expect(formatClarificationOptionReply(question, question.options[0]!)).toContain("Cost");
    expect(formatClarificationSkipReply(question)).toMatch(/^跳过澄清/);
    const formQ = parseClarificationQuestion(
      msg({
        id: "ask2",
        body: "form",
        meta: {
          op: "clarification_question",
          question_id: "qf",
          layout: "form",
          prompt: "form",
          fields: [{ id: "budget", label: "Budget", type: "text" }],
        },
      }),
    )!;
    expect(formatClarificationFormReply(formQ, { budget: "2000" })).toContain("Budget: 2000");
  });

  it("resolves answered and skipped from subsequent user messages", () => {
    const pending = resolveClarificationResolution(question, [
      msg({ id: "ask", body: "Pick a focus", meta: listMeta }),
    ]);
    expect(pending.status).toBe("pending");

    const answered = resolveClarificationResolution(question, [
      msg({ id: "ask", body: "Pick a focus", meta: listMeta }),
      msg({
        id: "u1",
        sender_type: "user",
        body: formatClarificationOptionReply(question, question.options[1]!),
        card_kind: "chat",
        created_at: "2026-08-02T10:01:00Z",
      }),
    ]);
    expect(answered.status).toBe("answered");
    if (answered.status === "answered") {
      expect(answered.optionLabel).toBe("Recall");
    }

    const skipped = resolveClarificationResolution(question, [
      msg({ id: "ask", body: "Pick a focus", meta: listMeta }),
      msg({
        id: "u2",
        sender_type: "user",
        body: formatClarificationSkipReply(question),
        card_kind: "chat",
        created_at: "2026-08-02T10:02:00Z",
      }),
    ]);
    expect(skipped.status).toBe("skipped");
  });
});
