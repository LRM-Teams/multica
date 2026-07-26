import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import type { ChannelMessage } from "@multica/core/types";
import { formatSystemEventPreviewText } from "./channel-system-event-preview-text";
import type { MentionPreviewResolver } from "./message-preview";

// Same fixed template set channel-system-event.test.tsx stubs for the JSX
// components — kept in sync deliberately so both surfaces are proven against
// identical copy, not two independently-drifting fakes.
const TEMPLATES = {
  message: {
    system_event: {
      member_added: "{actor} 邀请 {target} 加入了频道",
      member_added_no_actor: "{target} 加入了频道",
      member_removed: "{actor} 将 {target} 移出了频道",
      member_removed_no_actor: "{target} 被移出了频道",
      member_left: "{target} 退出了频道",
      issue: {
        actor_system: "Multica",
        created: "{actor} 创建了 {issue}",
        assigned: "{actor} 将 {issue} 指派给 {target}",
        assigned_unknown: "{actor} 重新指派了 {issue}",
        in_progress: "{actor} 开始了 {issue}",
        in_review: "{actor} 将 {issue} 移至「评审」",
        done: "{actor} 完成了 {issue}",
        updated: "{actor} 更新了 {issue}",
        status: "{actor} 将 {issue} 移至「{{status}}」",
        aggregate_created: "{actor} 创建了 {issues}",
        aggregate_done: "{actor} 完成了 {issues}",
        aggregate_assigned: "{actor} 指派了 {issues}",
        aggregate_started: "{actor} 开始了 {issues}",
        aggregate_in_review: "{actor} 将 {issues} 移至「评审」",
        aggregate_updated: "{actor} 更新了 {issues}",
      },
      issue_status: {
        backlog: "待办事项",
        todo: "待办",
        in_progress: "处理中",
        in_review: "待审",
        done: "已完成",
        blocked: "已阻塞",
        cancelled: "已取消",
      },
      project: {
        actor_system: "Multica",
        bound: "{actor} 把本群关联到项目「{project}」",
        changed: "{actor} 把关联项目从「{previous}」改为「{project}」",
        unbound: "{actor} 解除了与项目「{previous}」的关联",
      },
      reminder: {
        fired: "提醒已触发：{title}",
        anchor_unavailable_suffix: " · 来源不可用",
      },
      thread: {
        unfollowed: "{actor} 取消关注了此话题",
        followed: "{actor} 关注了此话题",
      },
      agent_dm: {
        paused_budget: "他们就「{matter}」来回到了上限（{round}/{roundLimit} 轮），已暂停。",
        paused_frequency: "{agentA} 和 {agentB} 私聊太频繁、已暂停这对。",
        paused_pair: "你已暂停这对智能体的私聊——他们不再互发，直到你恢复。",
        paused_global: "你暂停了涉及你智能体的所有私聊——它们暂时不再和任何智能体互发，直到你恢复。",
        resumed: "已恢复，你的智能体可以继续私聊了。",
      },
    },
  },
};

const t = ((selector: (r: typeof TEMPLATES) => string, options?: Record<string, unknown>) => {
  const raw = selector(TEMPLATES);
  // Mirror i18next's `{{ }}` interpolation; single-brace `{actor}`/`{issue}`
  // slots are left for fillSlots to substitute afterward.
  return options
    ? raw.replace(/\{\{(\w+)\}\}/g, (_match, key: string) => String(options[key] ?? ""))
    : raw;
}) as TFunction<"channels">;

const resolveMention: MentionPreviewResolver = (type, id, fallback) => {
  if (type === "agent" && id === "agent-fe") return "前端工程师";
  if (type === "member" && id === "user-1") return "Frank";
  return fallback;
};

function systemMessage(
  part: { event: string; params?: Record<string, unknown> } | undefined,
  overrides: Partial<ChannelMessage> = {},
): ChannelMessage {
  return {
    type: "system",
    parts:
      part === undefined
        ? undefined
        : [{ type: "system_event", event: part.event, event_params: part.params ?? {} }],
    content: "fallback content",
    ...overrides,
  } as ChannelMessage;
}

describe("formatSystemEventPreviewText", () => {
  it("returns null for a non-system message so the caller falls back to raw preview text", () => {
    expect(
      formatSystemEventPreviewText({ type: "agent", parts: undefined }, t, resolveMention),
    ).toBeNull();
  });

  it("returns null for a system message with no recognized system_event part", () => {
    expect(formatSystemEventPreviewText(systemMessage(undefined), t, resolveMention)).toBeNull();
  });

  it("localizes issue_created — the exact #634 bug report shape", () => {
    // Frank saw "system: LRM-191 created" in the sidebar while the in-channel
    // row correctly read "QA Bot 创建了 Issue LRM-191".
    const message = systemMessage({
      event: "issue_created",
      params: {
        issue_id: "issue-1",
        issue_identifier: "LRM-191",
        actor_id: "agent-fe",
        actor_type: "agent",
      },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "@前端工程师 创建了 LRM-191",
    );
  });

  it("prefers the issue title over the bare identifier, same as the in-channel row", () => {
    const message = systemMessage({
      event: "issue_created",
      params: {
        issue_id: "issue-1",
        issue_identifier: "LRM-191",
        issue_title: "Fix sidebar preview i18n",
        actor_id: "agent-fe",
        actor_type: "agent",
      },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "@前端工程师 创建了 Fix sidebar preview i18n",
    );
  });

  it("localizes a status transition via the shared issue_status label", () => {
    const message = systemMessage({
      event: "issue_status_changed",
      params: {
        issue_id: "issue-1",
        issue_identifier: "LRM-191",
        issue_status: "blocked",
        actor_id: "agent-fe",
        actor_type: "agent",
      },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "@前端工程师 将 LRM-191 移至「已阻塞」",
    );
  });

  it("degrades an unrecognized status to the generic 'updated' copy, never the raw enum", () => {
    const message = systemMessage({
      event: "issue_status_changed",
      params: {
        issue_id: "issue-1",
        issue_identifier: "LRM-191",
        issue_status: "some_future_status",
        actor_id: "agent-fe",
        actor_type: "agent",
      },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe("@前端工程师 更新了 LRM-191");
  });

  it("localizes an actor-less member_added row (system_invariant roster sync, #661)", () => {
    const message = systemMessage({
      event: "channel_member_added",
      params: { target_id: "agent-fe", target_type: "agent" },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe("@前端工程师 加入了频道");
  });

  it("localizes a member_added row with a real actor", () => {
    const message = systemMessage({
      event: "channel_member_added",
      params: {
        target_id: "agent-fe",
        target_type: "agent",
        actor_id: "user-1",
        actor_type: "human",
      },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "@Frank 邀请 @前端工程师 加入了频道",
    );
  });

  it("localizes a project bound row", () => {
    const message = systemMessage({
      event: "channel_project_bound",
      params: {
        project_id: "proj-1",
        project_title: "LRM 2.0",
        actor_id: "agent-fe",
        actor_type: "agent",
      },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "@前端工程师 把本群关联到项目「LRM 2.0」",
    );
  });

  it("localizes a reminder-fired row with the anchor-unavailable suffix", () => {
    const message = systemMessage({
      event: "reminder_fired",
      params: {
        reminder_id: "rem-1",
        occurrence_id: "occ-1",
        title: "Standup",
        anchor_available: false,
      },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "提醒已触发：Standup · 来源不可用",
    );
  });

  it("joins a server-aggregated issue event's items with the locale separator", () => {
    const message = systemMessage({
      event: "issue_created",
      params: {
        actor_id: "agent-fe",
        actor_type: "agent",
        items: [
          { issue_id: "issue-1", issue_identifier: "LRM-1" },
          { issue_id: "issue-2", issue_identifier: "LRM-2" },
        ],
      },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "@前端工程师 创建了 LRM-1、LRM-2",
    );
  });

  it("falls back to the 'Multica' system actor label when no typed actor fact exists", () => {
    const message = systemMessage({
      event: "issue_completed",
      params: { issue_id: "issue-1", issue_identifier: "LRM-191", issue_status: "done" },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe("Multica 完成了 LRM-191");
  });

  it("localizes thread_unfollowed with a resolved @display_name (LRM-540)", () => {
    const message = systemMessage({
      event: "thread_unfollowed",
      params: {
        actor_id: "agent-fe",
        actor_type: "agent",
        actor_handle: "qian-duan",
      },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "@前端工程师 取消关注了此话题",
    );
  });

  // #692 A2A gate rows — the DM-list sidebar preview must draw the same
  // localized copy the in-DM row composes, never the BE English fallback.
  const a2aParams = {
    exchange_id: "ex-1",
    dm_channel_id: "dm-1",
    matter_id: "m-1",
    matter: "定位登录超时",
    state: "paused_budget",
    round: 3,
    round_limit: 3,
    agent_a_id: "a1",
    agent_a_handle: "felix",
    agent_a_name: "Felix",
    agent_b_id: "a2",
    agent_b_handle: "barry",
    agent_b_name: "Barry",
    actions: ["view_dm", "grant_rounds", "pause_pair", "pause_global"],
  };

  it("localizes an A2A budget-exhaustion row with matter + rounds", () => {
    const message = systemMessage({ event: "agent_dm_paused_budget", params: a2aParams });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "他们就「定位登录超时」来回到了上限（3/3 轮），已暂停。",
    );
  });

  it("falls back to the BE「当前事项」matter verbatim when it is the only summary", () => {
    const message = systemMessage({
      event: "agent_dm_paused_budget",
      params: { ...a2aParams, matter: "当前事项" },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "他们就「当前事项」来回到了上限（3/3 轮），已暂停。",
    );
  });

  it("localizes an A2A frequency-pause row with both agent display names", () => {
    const message = systemMessage({ event: "agent_dm_paused_frequency", params: a2aParams });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "Felix 和 Barry 私聊太频繁、已暂停这对。",
    );
  });

  it("falls back to the @handle when an agent display name is absent", () => {
    const message = systemMessage({
      event: "agent_dm_paused_frequency",
      params: { ...a2aParams, agent_a_name: "", agent_b_name: "" },
    });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "felix 和 barry 私聊太频繁、已暂停这对。",
    );
  });

  it("localizes an owner pair-pause row (static copy)", () => {
    const message = systemMessage({ event: "agent_dm_paused_pair", params: a2aParams });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "你已暂停这对智能体的私聊——他们不再互发，直到你恢复。",
    );
  });

  it("localizes an owner global-pause row (static copy)", () => {
    const message = systemMessage({ event: "agent_dm_paused_global", params: a2aParams });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "你暂停了涉及你智能体的所有私聊——它们暂时不再和任何智能体互发，直到你恢复。",
    );
  });

  it("localizes a resume row (static copy)", () => {
    const message = systemMessage({ event: "agent_dm_resumed", params: a2aParams });
    expect(formatSystemEventPreviewText(message, t, resolveMention)).toBe(
      "已恢复，你的智能体可以继续私聊了。",
    );
  });
});
