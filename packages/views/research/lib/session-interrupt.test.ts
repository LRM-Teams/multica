// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchMessage } from "@multica/core/types";
import {
  isPostRetryWakeFailure,
  normalizeWakeReason,
  resolveSessionInterrupt,
  sanitizeWakeHeadline,
} from "./session-interrupt";

function msg(
  partial: Partial<ResearchMessage> & Pick<ResearchMessage, "id" | "created_at">,
): ResearchMessage {
  return {
    session_id: "sess",
    sender_type: "system",
    sender_id: null,
    target_agent_id: null,
    body: "",
    card_kind: "process",
    meta: {},
    ...partial,
  };
}

describe("sanitizeWakeHeadline", () => {
  it("strips stack frames and keeps a readable first line", () => {
    const raw = [
      "目标 agent 的 runtime/daemon 可能离线",
      "    at wakeAgent (/app/server/wake.go:42)",
      "    at Dispatch (/app/server/dispatch.go:10)",
    ].join("\n");
    expect(sanitizeWakeHeadline(raw)).toBe("目标 agent 的 runtime/daemon 可能离线");
  });
});

describe("normalizeWakeReason", () => {
  it("maps known codes and offline synonyms", () => {
    expect(normalizeWakeReason("runtime_offline")).toBe("runtime_offline");
    expect(normalizeWakeReason("daemon disconnected")).toBe("runtime_offline");
    expect(normalizeWakeReason("agent model is required")).toBe("agent_model_required");
    expect(normalizeWakeReason("weird")).toBe("unknown");
  });
});

describe("resolveSessionInterrupt", () => {
  it("returns null when latest process event is not wake_failed", () => {
    const messages = [
      msg({
        id: "1",
        created_at: "2026-08-02T10:00:00Z",
        body: "Scout wake failed",
        meta: { op: "wake_failed", reason: "runtime_offline" },
      }),
      msg({
        id: "2",
        created_at: "2026-08-02T10:01:00Z",
        body: "graph updated",
        meta: { op: "graph_append" },
      }),
    ];
    expect(resolveSessionInterrupt(messages)).toBeNull();
  });

  it("surfaces active wake_failed with sanitized copy (LRM-823)", () => {
    const interrupt = resolveSessionInterrupt([
      msg({
        id: "chat-1",
        created_at: "2026-08-02T10:00:00Z",
        card_kind: "chat",
        sender_type: "user",
        body: "go",
      }),
      msg({
        id: "wake-1",
        created_at: "2026-08-02T10:02:00Z",
        body: "目标 agent 不是调研团成员\n    at RequireActiveMember",
        meta: {
          op: "wake_failed",
          reason: "fleet_member_not_found",
          recovery_hint: "请改派给罗纳尔多或其他在册成员。",
        },
      }),
    ]);
    expect(interrupt).toMatchObject({
      messageId: "wake-1",
      reason: "fleet_member_not_found",
      headline: "目标 agent 不是调研团成员",
      recoveryHint: "请改派给罗纳尔多或其他在册成员。",
    });
  });
});

describe("isPostRetryWakeFailure", () => {
  it("detects a newer tip wake_failed than the one retried", () => {
    const messages = [
      msg({
        id: "w1",
        created_at: "2026-08-02T10:05:00Z",
        meta: { op: "wake_failed", reason: "runtime_offline" },
      }),
      msg({
        id: "w2",
        created_at: "2026-08-02T10:06:00Z",
        meta: { op: "wake_failed", reason: "runtime_offline" },
      }),
    ];
    expect(isPostRetryWakeFailure(messages, "w1")).toBe(true);
    expect(isPostRetryWakeFailure(messages, "w2")).toBe(false);
    expect(isPostRetryWakeFailure(messages, null)).toBe(false);
  });
});
