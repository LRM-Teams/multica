import { describe, expect, it } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import {
  CONTENT_FACE_COPY_ZH,
  readNodeContentFaces,
  resolveContentFaceValue,
  resolveContentFaceValues,
} from "./research-node-content-faces";

function node(
  partial: Partial<ResearchGraphNode> &
    Pick<ResearchGraphNode, "id" | "status"> & {
      content?: {
        goal?: string;
        operation_approach?: string;
        research_approach?: string;
        result?: string;
      };
    },
): ResearchGraphNode {
  return {
    id: partial.id,
    session_id: "s1",
    node_type: "finding",
    title: partial.title ?? "节点",
    summary: partial.summary ?? "SHOULD_NOT_USE_SUMMARY",
    status: partial.status,
    actor_agent_id: null,
    payload: partial.payload ?? { secret: "nope" },
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    ...(partial.content ? { content: partial.content } : {}),
  } as ResearchGraphNode;
}

describe("readNodeContentFaces (LRM-1332)", () => {
  it("reads only projected content.* and never summary/payload", () => {
    const faces = readNodeContentFaces(
      node({
        id: "n1",
        status: "completed",
        summary: "summary leak",
        payload: { goal: "payload leak", result: "payload result" },
        content: {
          goal: "  目标文  ",
          operation_approach: "操作",
          research_approach: "",
          result: "结果",
        },
      }),
    );
    expect(faces).toEqual({
      goal: "目标文",
      operation_approach: "操作",
      research_approach: "",
      result: "结果",
    });
  });

  it("treats missing content projection as empty faces", () => {
    expect(readNodeContentFaces(node({ id: "n2", status: "running" }))).toEqual({
      goal: "",
      operation_approach: "",
      research_approach: "",
      result: "",
    });
  });
});

describe("resolveContentFaceValue", () => {
  const copy = CONTENT_FACE_COPY_ZH;

  it("passes through real values", () => {
    expect(resolveContentFaceValue("goal", "真实目标", "running", copy)).toBe(
      "真实目标",
    );
  });

  it("uses 未提供 for missing non-result fields", () => {
    expect(resolveContentFaceValue("goal", "", "completed", copy)).toBe("未提供");
  });

  it("uses 结果整理中 when running and result empty", () => {
    expect(resolveContentFaceValue("result", "", "running", copy)).toBe(
      "结果整理中",
    );
    expect(resolveContentFaceValue("result", "", "active", copy)).toBe(
      "结果整理中",
    );
  });

  it("uses failed copy without exposing error codes", () => {
    expect(resolveContentFaceValue("result", "", "failed", copy)).toBe(
      "本轮未产出可展示结果",
    );
  });
});

describe("resolveContentFaceValues", () => {
  it("does not invent values from summary when content empty", () => {
    const values = resolveContentFaceValues(
      node({ id: "n3", status: "completed", summary: "from summary" }),
      "surface",
      CONTENT_FACE_COPY_ZH,
    );
    expect(values.goal).toBe("未提供");
    expect(values.result).toBe("未提供");
    expect(Object.values(values).join(" ")).not.toContain("from summary");
  });
});
