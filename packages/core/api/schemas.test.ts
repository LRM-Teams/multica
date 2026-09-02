import { describe, expect, it } from "vitest";
import {
  DashboardAgentRunTimeListSchema,
  DashboardUsageByAgentListSchema,
  DashboardUsageDailyListSchema,
  AgentRuntimeSchema,
  ComputerConnectionListSchema,
  ComputerConnectionSchema,
  ComputerWorkJournalSettingSchema,
  ComputerCollectRootsSchema,
  EMPTY_COMPUTER_CONNECTION_LIST,
  EMPTY_COMPUTER_WORK_JOURNAL_SETTING,
  EMPTY_COMPUTER_COLLECT_ROOTS,
  ChannelCreateErrorBodySchema,
  DuplicateIssueErrorBodySchema,
  EMPTY_EVOLUTION_REVIEW_SUBMISSION_LIST,
  EMPTY_WORKSPACE_MEMORY_CURATION_STATUS,
  ChannelMessagesPageSchema,
  ChannelThreadMessagesPageSchema,
  ChannelMessageSearchResponseSchema,
  AgentFileContentResponseSchema,
  AgentFilesResponseSchema,
  EMPTY_AGENT_FILE_CONTENT_RESPONSE,
  EMPTY_AGENT_FILES_RESPONSE,
  EMPTY_USER,
  EvolutionReviewSubmissionListSchema,
  EnsureWindyResponseSchema,
  EnsurePeriodBriefAgentResponseSchema,
  WorkspaceMemoryCurationStatusSchema,
  MemoryCuratorProfileSchema,
  StartMemoryCurationRunResponseSchema,
  MemoryCurationBackfillResponseSchema,
  ListIssuesResponseSchema,
  RuntimeHourlyActivityListSchema,
  RuntimeUsageByAgentListSchema,
  RuntimeUsageByHourListSchema,
  RuntimeUsageListSchema,
  WorkspaceSchema,
  WorkspaceListSchema,
  EMPTY_WORKSPACE,
  RuntimeAgentWorkspacesResponseSchema,
  EMPTY_RUNTIME_AGENT_WORKSPACES_RESPONSE,
  StickerCatalogResponseSchema,
  UserSchema,
  ConversationHandleLookupSchema,
  EMPTY_CONVERSATION_HANDLE_LOOKUP,
  SandboxNodeTemplatesResponseSchema,
  EMPTY_SANDBOX_NODE_TEMPLATES_RESPONSE,
  VoiceTranscriptResponseSchema,
  EMPTY_VOICE_TRANSCRIPT_RESPONSE,
  RunnerActivityResponseSchema,
  RunnerActivitySummariesResponseSchema,
  EMPTY_RUNNER_ACTIVITY_SUMMARIES_RESPONSE,
  SendChatMessageResponseSchema,
  EMPTY_SEND_CHAT_MESSAGE_RESPONSE,
  ChannelMentionCandidatesResponseSchema,
  EMPTY_CHANNEL_MENTION_CANDIDATES,
} from "./schemas";
import { parseWithFallback } from "./schema";

const baseIssue = {
  id: "11111111-1111-1111-1111-111111111111",
  workspace_id: "ws-1",
  number: 1,
  identifier: "MUL-1",
  title: "Test",
  description: null,
  status: "todo",
  priority: "medium",
  assignee_type: null,
  assignee_id: null,
  creator_type: "member",
  creator_id: "user-1",
  parent_issue_id: null,
  project_id: null,
  position: 0,
  start_date: null,
  due_date: null,
  metadata: {},
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

describe("AgentRuntimeSchema detect-only update status", () => {
  it("accepts the current auto-detection source and available outcome", () => {
    const parsed = AgentRuntimeSchema.parse({
      id: "runtime-1",
      workspace_id: "workspace-1",
      daemon_id: "daemon-1",
      name: "Codex",
      runtime_mode: "local",
      provider: "codex",
      status: "online",
      device_info: "",
      metadata: {},
      current_version: "0.4.24-alpha.9",
      update_state: "idle",
      runtime_health: "update_available",
      auto_update: {
        session_id: "session-1",
        revision: 2,
        observed_at: "2026-08-11T13:17:49Z",
        auto_update_effective_enabled: false,
        config_source: "auto_detect",
        ineligible_reason: null,
        check_interval_seconds: 300,
        phase: "waiting",
        attempt_source: "auto",
        last_attempt_at: "2026-08-11T13:17:49Z",
        last_outcome: "update_available",
        target_version: "v0.4.24-alpha.11",
        error_code: null,
        error_message: null,
        staged_version: null,
        activation_generation: null,
        received_at: "2026-08-11T13:17:50Z",
        updated_at: "2026-08-11T13:17:50Z",
      },
      owner_id: "user-1",
      last_seen_at: "2026-08-11T13:17:50Z",
      created_at: "2026-08-11T07:33:19Z",
      updated_at: "2026-08-11T13:17:50Z",
    });

    expect(parsed.auto_update).toMatchObject({
      auto_update_effective_enabled: false,
      config_source: "auto_detect",
      last_outcome: "update_available",
      target_version: "v0.4.24-alpha.11",
    });
  });
});

describe("VoiceTranscriptResponseSchema", () => {
  it("accepts only the bounded transcript envelope", () => {
    expect(VoiceTranscriptResponseSchema.parse({ text: "你好" })).toEqual({ text: "你好" });
    expect(
      parseWithFallback(
        { text: 123 },
        VoiceTranscriptResponseSchema,
        EMPTY_VOICE_TRANSCRIPT_RESPONSE,
        { endpoint: "POST /api/voice/asr" },
      ),
    ).toEqual(EMPTY_VOICE_TRANSCRIPT_RESPONSE);
  });
});

describe("RunnerActivityResponseSchema", () => {
  it("preserves unknown lifecycle facts and bounded text", () => {
    const parsed = RunnerActivityResponseSchema.parse({
      summary: { label: "Future state", activityKind: "future", detailKind: "future_detail" },
      timeline: [{
        id: "row-1",
        occurred_at: "2026-08-06T00:00:00Z",
        title: "Future row",
        activity_kind: "future",
        detail_kind: "future_detail",
        body_kind: "future-body",
      }],
    });
    expect(parsed.summary?.label).toBe("Future state");
    expect(parsed.timeline[0]).toMatchObject({ title: "Future row", body_kind: "future-body" });
  });
});

describe("RunnerActivitySummariesResponseSchema", () => {
  it("parses the sparse Workspace projection and fails closed on malformed items", () => {
    expect(RunnerActivitySummariesResponseSchema.parse({})).toEqual({ items: [] });
    expect(RunnerActivitySummariesResponseSchema.parse({
      items: [{
        agent_id: "agent-1",
        summary: { label: "Online", activityKind: "online", detailKind: "idle" },
      }],
    })).toEqual({
      items: [{
        agent_id: "agent-1",
        summary: { label: "Online", activityKind: "online", detailKind: "idle" },
      }],
    });

    expect(parseWithFallback(
      { items: [{ agent_id: 42, summary: null }] },
      RunnerActivitySummariesResponseSchema,
      EMPTY_RUNNER_ACTIVITY_SUMMARIES_RESPONSE,
      { endpoint: "GET /api/members/agents/runner-activity-summaries" },
    )).toEqual({ items: [] });
  });
});

describe("IssueSchema (via ListIssuesResponseSchema)", () => {
  it("accepts a primitive metadata KV map", () => {
    const payload = {
      issues: [
        {
          ...baseIssue,
          metadata: { pipeline_status: "waiting", pr_number: 3, is_blocked: true },
        },
      ],
      total: 1,
    };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({
      pipeline_status: "waiting",
      pr_number: 3,
      is_blocked: true,
    });
  });

  it("defaults metadata to {} when the server omits it (older backend)", () => {
    const { metadata: _omit, ...issueWithoutMetadata } = baseIssue;
    const payload = { issues: [issueWithoutMetadata], total: 1 };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({});
  });

  it("rejects metadata with non-primitive values (nested object)", () => {
    const payload = {
      issues: [{ ...baseIssue, metadata: { nested: { x: 1 } } }],
      total: 1,
    };
    expect(ListIssuesResponseSchema.safeParse(payload).success).toBe(false);
  });
});

describe("Agent file schemas", () => {
  it("parses a valid file tree response", () => {
    const parsed = AgentFilesResponseSchema.parse({
      agent_id: "agent-1",
      status: "ok",
      nodes: [{ path: "memory/MEMORY.md", is_dir: false, size: 42 }],
      truncated: false,
      root_path: "/home/u/.multica/workspaces/ws/agents/agent-1",
    });
    expect(parsed.nodes[0]?.path).toBe("memory/MEMORY.md");
    expect(parsed.root_path).toBe("/home/u/.multica/workspaces/ws/agents/agent-1");
  });

  it("defaults a missing root_path so older daemons still parse", () => {
    const parsed = AgentFilesResponseSchema.parse({
      agent_id: "agent-1",
      status: "ok",
      nodes: [],
    });
    expect(parsed.root_path).toBe("");
  });

  it("falls back when file tree response is malformed", () => {
    const parsed = parseWithFallback(
      { agent_id: "agent-1", status: "ok", nodes: null },
      AgentFilesResponseSchema,
      EMPTY_AGENT_FILES_RESPONSE,
      { endpoint: "GET /api/members/agents/:id/files" },
    );
    expect(parsed).toEqual(EMPTY_AGENT_FILES_RESPONSE);
  });

  it("falls back when file content response is malformed", () => {
    const parsed = parseWithFallback(
      { content: 123, content_hash: null },
      AgentFileContentResponseSchema,
      EMPTY_AGENT_FILE_CONTENT_RESPONSE,
      { endpoint: "GET /api/members/agents/:id/files/content" },
    );
    expect(parsed).toEqual(EMPTY_AGENT_FILE_CONTENT_RESPONSE);
  });
});

// The duplicate-issue branch in create-issue.tsx feeds ApiError.body
// (typed as `unknown`) through this schema. Any future server drift that
// loses the contract MUST fail the parse so the UI falls back to a normal
// error toast instead of rendering an empty / partial duplicate card.
describe("DuplicateIssueErrorBodySchema", () => {
  const valid = {
    code: "active_duplicate_issue",
    error: "An active issue with this title already exists: MUL-12 – Login bug",
    issue: {
      id: "11111111-1111-1111-1111-111111111111",
      identifier: "MUL-12",
      title: "Login bug",
    },
  };

  it("accepts a well-formed body", () => {
    expect(DuplicateIssueErrorBodySchema.safeParse(valid).success).toBe(true);
  });

  it("accepts unknown extra fields via .loose()", () => {
    const forwardCompat = {
      ...valid,
      hint: "Try a different title",
      issue: { ...valid.issue, workspace_id: "ws-1", status: "todo" },
    };
    expect(DuplicateIssueErrorBodySchema.safeParse(forwardCompat).success).toBe(true);
  });

  it("rejects a renamed code (so renames degrade to the generic toast)", () => {
    const renamed = { ...valid, code: "duplicate_issue" };
    expect(DuplicateIssueErrorBodySchema.safeParse(renamed).success).toBe(false);
  });

  it("rejects a missing issue object", () => {
    const { issue: _omit, ...without } = valid;
    expect(DuplicateIssueErrorBodySchema.safeParse(without).success).toBe(false);
  });

  it("rejects a non-string issue.id", () => {
    const broken = { ...valid, issue: { ...valid.issue, id: 42 } };
    expect(DuplicateIssueErrorBodySchema.safeParse(broken).success).toBe(false);
  });

  it("accepts a missing error field (it is optional)", () => {
    const { error: _omit, ...without } = valid;
    expect(DuplicateIssueErrorBodySchema.safeParse(without).success).toBe(true);
  });
});

describe("ChannelCreateErrorBodySchema", () => {
  it("accepts a well-formed duplicate-name body", () => {
    const valid = { code: "channel_name_taken", error: "channel name already exists" };
    expect(ChannelCreateErrorBodySchema.safeParse(valid).success).toBe(true);
  });

  it("accepts a missing error field (it is optional)", () => {
    expect(ChannelCreateErrorBodySchema.safeParse({ code: "channel_name_taken" }).success).toBe(true);
  });

  it("accepts unknown extra fields via .loose()", () => {
    const forwardCompat = { code: "channel_name_taken", error: "x", workspace_id: "ws-1" };
    expect(ChannelCreateErrorBodySchema.safeParse(forwardCompat).success).toBe(true);
  });

  it("rejects a renamed code (so renames degrade to the generic toast)", () => {
    expect(ChannelCreateErrorBodySchema.safeParse({ code: "name_taken" }).success).toBe(false);
  });

  it("rejects a missing code", () => {
    expect(ChannelCreateErrorBodySchema.safeParse({ error: "channel name already exists" }).success).toBe(false);
  });
});

// `user.timezone` (Viewing tz) was added in the timezone-architecture RFC.
// A desktop build older than the server — or a server predating the
// `user.timezone` migration — will return a `/api/me` body with no
// `timezone` key. The schema must not fail closed on that: the field
// defaults to `null`, which the frontend resolves to the browser-detected
// tz at render time.
describe("UserSchema timezone drift", () => {
  const base = {
    id: "11111111-1111-1111-1111-111111111111",
    name: "Ada",
    email: "ada@example.com",
  };

  it("defaults timezone to null when the field is absent", () => {
    const parsed = UserSchema.parse(base);
    expect(parsed.timezone).toBe(null);
  });

  it("preserves an explicit IANA timezone", () => {
    const parsed = UserSchema.parse({ ...base, timezone: "Asia/Tokyo" });
    expect(parsed.timezone).toBe("Asia/Tokyo");
  });

  it("accepts an explicit null timezone", () => {
    const parsed = UserSchema.parse({ ...base, timezone: null });
    expect(parsed.timezone).toBe(null);
  });

  // Wrong-type drift: a future server bug sending `timezone` as a number
  // must not throw into the UI. parseWithFallback degrades the whole user
  // object to the explicit fallback (EMPTY_USER) so /api/me callers keep a
  // valid shape instead of white-screening.
  it("falls back to EMPTY_USER when timezone is the wrong type", () => {
    const parsed = parseWithFallback(
      { ...base, timezone: 42 },
      UserSchema,
      EMPTY_USER,
      { endpoint: "GET /api/me" },
    );
    expect(parsed).toBe(EMPTY_USER);
  });
});

describe("EvolutionReviewSubmissionListSchema drift", () => {
  const base = {
    id: "sub-1",
    workspace_id: "ws-1",
    source_agent_id: "agent-1",
    unit_type: "memory",
    local_unit_id: "local-1",
    title: "Targeted tests",
    summary: "Run narrow tests first.",
    content_hash: "hash",
    sensitivity: "none",
    confidence: "high",
    status: "needs_review",
    review_decision: "needs_review",
    review_risk_level: "medium",
    review_reason: "reviewer requested manual review",
  };

  it("defaults optional arrays and metadata so the review queue can render older rows", () => {
    const parsed = EvolutionReviewSubmissionListSchema.parse([base]);
    expect(parsed[0]?.tags).toEqual([]);
    expect(parsed[0]?.review_metadata).toEqual({});
    expect(parsed[0]?.evidence).toEqual({ source: "", source_date: "", evidence_refs: [] });
    expect(parsed[0]?.applies).toEqual({ scope: "", tags: [], tools: [], task_types: [], project_types: [], languages: [], frameworks: [] });
    expect(parsed[0]?.materialized_skill).toBeUndefined();
    expect(parsed[0]?.files).toBeUndefined();
  });

  it("parses evidence and materialized skill details for the review UI", () => {
    const parsed = EvolutionReviewSubmissionListSchema.parse([
      {
        ...base,
        evidence: { source: "memory_curation_l3", source_date: "2026-07-10", evidence_refs: ["issue"] },
        applies: { scope: "workspace", languages: ["go"] },
        materialized_skill: { id: "skill-1", name: "targeted-tests" },
      },
    ]);
    expect(parsed[0]?.evidence).toEqual({ source: "memory_curation_l3", source_date: "2026-07-10", evidence_refs: ["issue"] });
    expect(parsed[0]?.applies.languages).toEqual(["go"]);
    expect(parsed[0]?.materialized_skill).toEqual({ id: "skill-1", name: "targeted-tests", description: "" });
  });

  it("defaults null and wrong-type nested fields without dropping the submission", () => {
    const parsed = EvolutionReviewSubmissionListSchema.parse([{
      ...base,
      evidence: null,
      applies: "future-shape",
      tags: null,
      review_metadata: [],
      files: "future-shape",
      materialized_skill: null,
    }]);
    expect(parsed[0]?.evidence.evidence_refs).toEqual([]);
    expect(parsed[0]?.applies.languages).toEqual([]);
    expect(parsed[0]?.tags).toEqual([]);
    expect(parsed[0]?.review_metadata).toEqual({});
    expect(parsed[0]?.files).toBeUndefined();
    expect(parsed[0]?.materialized_skill).toBeUndefined();
  });

  it("keeps unknown enum values as strings instead of failing the whole queue", () => {
    const parsed = EvolutionReviewSubmissionListSchema.parse([
      { ...base, status: "archived", review_risk_level: "critical" },
    ]);
    expect(parsed[0]?.status).toBe("archived");
    expect(parsed[0]?.review_risk_level).toBe("critical");
  });

  it("falls back to an empty queue when the body is not an array", () => {
    const parsed = parseWithFallback(
      { submissions: [base] },
      EvolutionReviewSubmissionListSchema,
      EMPTY_EVOLUTION_REVIEW_SUBMISSION_LIST,
      { endpoint: "GET /api/evolution/submissions" },
    );
    expect(parsed).toBe(EMPTY_EVOLUTION_REVIEW_SUBMISSION_LIST);
  });
});

describe("SendChatMessageResponseSchema", () => {
  it("keeps Raft pending when task_id is empty", () => {
    const parsed = SendChatMessageResponseSchema.parse({
      message_id: "m1",
      delivery_id: "chat:m1:agent:a1",
      pending: true,
      created_at: "2026-08-13T00:00:00Z",
    });
    expect(parsed.pending).toBe(true);
    expect(parsed.delivery_id).toBe("chat:m1:agent:a1");
    expect("task_id" in parsed).toBe(false);
  });

  it("treats a greeting-shaped body as not pending", () => {
    const parsed = SendChatMessageResponseSchema.parse({
      message_id: "m1",
      pending: false,
      created_at: "2026-08-13T00:00:00Z",
    });
    expect(parsed.pending).toBe(false);
  });

  it("falls back when the body is malformed", () => {
    expect(
      parseWithFallback(
        { message_id: 1, pending: "yes" },
        SendChatMessageResponseSchema,
        EMPTY_SEND_CHAT_MESSAGE_RESPONSE,
        { endpoint: "POST /api/chat/sessions/:id/messages" },
      ),
    ).toEqual(EMPTY_SEND_CHAT_MESSAGE_RESPONSE);
  });
});

describe("WorkspaceMemoryCurationStatusSchema drift", () => {
  it("defaults missing counters in stage stats", () => {
    const parsed = WorkspaceMemoryCurationStatusSchema.parse({
      workspace_id: "ws-1",
      stages: [{ id: "run-1", stage: "l3_promote", stats: { entries_promoted: 2 } }],
    });
    expect(parsed.pending_runs).toBe(0);
    expect(parsed.stages[0]?.stats.entries_promoted).toBe(2);
    expect(parsed.stages[0]?.stats.shared_candidates_synced).toBe(0);
  });

  it("falls back when the stage collection has the wrong type", () => {
    const parsed = parseWithFallback(
      { workspace_id: "ws-1", stages: null },
      WorkspaceMemoryCurationStatusSchema,
      EMPTY_WORKSPACE_MEMORY_CURATION_STATUS,
      { endpoint: "GET /api/workspaces/{id}/memory-curation/status" },
    );
    expect(parsed).toBe(EMPTY_WORKSPACE_MEMORY_CURATION_STATUS);
  });
});

describe("MemoryCuratorProfileSchema drift", () => {
  it("fills safe defaults for a profile returned by an older backend", () => {
    const parsed = MemoryCuratorProfileSchema.parse({
      id: "profile-1",
      workspace_id: "ws-1",
      user_id: "user-1",
    });
    expect(parsed).toMatchObject({
      enabled: false,
      mode: "review",
      target_scope: "owned_all",
      target_agent_ids: [],
      timezone: "Asia/Shanghai",
      confidence_threshold: 0.8,
    });
  });

  it("preserves waiting_runtime manual-run responses", () => {
    expect(StartMemoryCurationRunResponseSchema.parse({ id: "run-1", status: "waiting_runtime" }))
      .toEqual({ id: "run-1", status: "waiting_runtime" });
  });

  it("tolerates sparse memory curation backfill responses", () => {
    expect(MemoryCurationBackfillResponseSchema.parse({})).toMatchObject({
      since: "",
      until: "",
      dry_run: false,
      queued: [],
      skipped: [],
      queued_days: 0,
      skip_days: 0,
    });
  });
});

describe("ChannelMessageSearchResponseSchema", () => {
  it("keeps a valid search result and unknown future fields", () => {
    const parsed = ChannelMessageSearchResponseSchema.parse({
      query: "deploy",
      total: 1,
      results: [
        {
          message_id: "11111111-1111-1111-1111-111111111111",
          channel_id: "22222222-2222-2222-2222-222222222222",
          type: "user",
          author_id: null,
          author_name: "Ada",
          content: "deploy is ready",
          created_at: "2026-06-27T00:00:00Z",
          snippet: "future field",
        },
      ],
    });
    expect(parsed.results[0]?.content).toBe("deploy is ready");
    expect(parsed.results[0]?.snippet).toBe("future field");
  });

  it("defaults an older empty response shape", () => {
    const parsed = ChannelMessageSearchResponseSchema.parse({});
    expect(parsed.query).toBe("");
    expect(parsed.total).toBe(0);
    expect(parsed.results).toEqual([]);
  });

  it("rejects a non-array result list so callers can fall back", () => {
    expect(ChannelMessageSearchResponseSchema.safeParse({ results: null }).success).toBe(false);
  });
});

describe("Channel message pagination schemas", () => {
  it("strips legacy avatar snapshots from the message read model", () => {
    const parsed = ChannelMessagesPageSchema.parse({
      messages: [
        {
          id: "msg-1",
          channel_id: "channel-1",
          workspace_id: "ws-1",
          type: "agent",
          author_id: "agent-1",
          author_name: "Ronan",
          author_avatar_url: "/legacy/message.png",
          content: "hello",
          source: "multica",
          external_message_id: null,
          created_at: "2026-07-03T00:00:00Z",
          reply_to: {
            id: "msg-0",
            type: "agent",
            author_id: "agent-1",
            author_name: "Ronan",
            author_avatar_url: "/legacy/reply.png",
            content: "parent",
            created_at: "2026-07-03T00:00:00Z",
          },
          quote: {
            messageId: "msg-0",
            status: "active",
            snapshot: {
              type: "agent",
              authorId: "agent-1",
              authorName: "Ronan",
              authorAvatarUrl: "/legacy/quote.png",
              content: "parent",
              createdAt: "2026-07-03T00:00:00Z",
              selectedText: "chosen excerpt",
            },
          },
        },
      ],
    });

    const message = parsed.messages[0] as Record<string, unknown>;
    expect(message).not.toHaveProperty("author_avatar_url");
    expect(message.reply_to).not.toHaveProperty("author_avatar_url");
    expect((message.quote as { snapshot: Record<string, unknown> }).snapshot).not.toHaveProperty(
      "authorAvatarUrl",
    );
    expect((message.quote as { snapshot: Record<string, unknown> }).snapshot.selectedText).toBe(
      "chosen excerpt",
    );
  });

  it("keeps page cursor metadata and unknown future fields", () => {
    const parsed = ChannelMessagesPageSchema.parse({
      messages: [
        {
          id: "msg-1",
          channel_id: "channel-1",
          workspace_id: "ws-1",
          type: "user",
          author_id: null,
          author_name: "Ada",
          content: "hello",
          source: "multica",
          external_message_id: null,
          created_at: "2026-07-03T00:00:00Z",
          future: "kept",
        },
      ],
      limit: 50,
      has_more: true,
      next_cursor: { seq: 42, created_at: "2026-07-03T00:00:00Z", id: "msg-1" },
    });
    expect(parsed.messages[0]?.future).toBe("kept");
    expect(parsed.next_cursor?.seq).toBe(42);
  });

  it("keeps thread participants with defensive defaults", () => {
    const parsed = ChannelThreadMessagesPageSchema.parse({
      messages: [
        {
          id: "root-1",
          channel_id: "channel-1",
          workspace_id: "ws-1",
          type: "user",
          author_id: "user-1",
          author_name: "Ada",
          content: "root",
          source: "multica",
          external_message_id: null,
          created_at: "2026-07-03T00:00:00Z",
          thread_participants: [
            {
              key: "agent:agent-1",
              member_type: "agent",
              member_id: "agent-1",
              display_name: "Ronan",
            },
          ],
        },
      ],
    });
    expect(parsed.messages[0]?.thread_participants?.[0]).toMatchObject({
      key: "agent:agent-1",
      name: "",
      followed: false,
    });
  });

  it("rejects malformed message lists so callers can fall back", () => {
    expect(ChannelMessagesPageSchema.safeParse({ messages: null }).success).toBe(false);
    expect(ChannelThreadMessagesPageSchema.safeParse({ messages: null }).success).toBe(false);
  });

  it("rejects malformed selected quote text", () => {
    const result = ChannelMessagesPageSchema.safeParse({
      messages: [{
        id: "msg-1",
        channel_id: "channel-1",
        workspace_id: "ws-1",
        type: "user",
        author_id: "user-1",
        author_name: "Ada",
        content: "reply",
        source: "multica",
        external_message_id: null,
        created_at: "2026-07-03T00:00:00Z",
        quote_message_id: "msg-0",
        quote: {
          messageId: "msg-0",
          status: "active",
          snapshot: {
            type: "user",
            authorId: "user-2",
            authorName: "Bob",
            content: "source",
            createdAt: "2026-07-03T00:00:00Z",
            selectedText: 42,
          },
        },
      }],
    });

    expect(result.success).toBe(false);
  });
});

describe("StickerCatalogResponseSchema", () => {
  it("keeps sticker pack assets and defaults optional metadata", () => {
    const parsed = StickerCatalogResponseSchema.parse({
      packs: [
        {
          id: "builtin",
          stickers: [
            {
              pack_id: "builtin",
              sticker_id: "hi",
              asset_url: "/api/stickers/hi",
              alt: "Hi sticker",
            },
          ],
        },
      ],
    });

    expect(parsed.packs[0]?.id).toBe("builtin");
    expect(parsed.packs[0]?.stickers[0]?.sticker_id).toBe("hi");
    expect(parsed.packs[0]?.stickers[0]?.tags).toEqual([]);
    expect(parsed.packs[0]?.stickers[0]?.animated).toBe(false);
  });

  it("rejects a non-array pack list so callers can fall back", () => {
    expect(StickerCatalogResponseSchema.safeParse({ packs: null }).success).toBe(false);
  });
});

// The workspace dashboard and runtime-detail pages were re-pointed at the
// unified `task_usage_hourly` rollup. Every numeric field drives chart /
// KPI math, and string keys (date / agent_id / model) bucket the series.
// The contract these schemas must hold: a row missing a field degrades
// that field to a sane default rather than dropping the WHOLE array to
// the `[]` fallback — one drifted row must not blank the entire chart.
describe("dashboard + runtime usage schema drift", () => {
  it("coerces a missing numeric field to 0 instead of dropping the array", () => {
    const parsed = DashboardUsageDailyListSchema.parse([
      { date: "2026-05-19", model: "claude-opus-4-7", input_tokens: 100 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.output_tokens).toBe(0);
    expect(parsed[0]?.cache_read_tokens).toBe(0);
    expect(parsed[0]?.cache_write_tokens).toBe(0);
  });

  it("coerces a missing date key to \"\" so the rest of the series survives", () => {
    const parsed = DashboardUsageDailyListSchema.parse([
      { model: "claude-opus-4-7", input_tokens: 5 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.date).toBe("");
  });

  it("coerces a missing agent_id key to \"\" for the agent-runtime panel", () => {
    const parsed = DashboardAgentRunTimeListSchema.parse([
      { total_seconds: 42, task_count: 3, failed_count: 0 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.agent_id).toBe("");
  });

  it("coerces a missing agent_id key to \"\" for the usage-by-agent panel", () => {
    const parsed = DashboardUsageByAgentListSchema.parse([
      { model: "claude-opus-4-7", input_tokens: 7 },
    ]);
    expect(parsed[0]?.agent_id).toBe("");
  });

  it("coerces missing fields on every runtime usage schema", () => {
    expect(RuntimeUsageListSchema.parse([{ date: "2026-05-19" }])[0]?.input_tokens).toBe(0);
    expect(RuntimeHourlyActivityListSchema.parse([{ hour: 9 }])[0]?.count).toBe(0);
    expect(RuntimeUsageByAgentListSchema.parse([{ model: "x" }])[0]?.agent_id).toBe("");
    expect(RuntimeUsageByHourListSchema.parse([{ hour: 9 }])[0]?.model).toBe("");
  });

  it("rejects a non-array body so parseWithFallback can return its fallback", () => {
    expect(DashboardUsageDailyListSchema.safeParse(null).success).toBe(false);
    expect(RuntimeUsageListSchema.safeParse({ rows: [] }).success).toBe(false);
  });

  it("keeps unknown server-side fields via .loose()", () => {
    const parsed = RuntimeUsageListSchema.parse([
      { date: "2026-05-19", region: "us-east" },
    ]);
    expect((parsed[0] as Record<string, unknown>).region).toBe("us-east");
  });
});

describe("RuntimeAgentWorkspacesResponseSchema", () => {
  it("parses the canonical agent workspace response", () => {
    const parsed = RuntimeAgentWorkspacesResponseSchema.parse({
      runtime_id: "runtime-1",
      status: "ok",
      items: [
        {
          dir_name: "agent-1",
          rel_path: "workspace-1/agents/agent-1",
          agent_id: "agent-1",
          agent_name: "Alice",
          orphan: false,
        },
      ],
    });
    expect(parsed.items[0]?.rel_path).toBe("workspace-1/agents/agent-1");
  });

  it("falls back when required response fields are malformed", () => {
    expect(
      parseWithFallback(
        { runtime_id: "runtime-1", status: "ok", items: null },
        RuntimeAgentWorkspacesResponseSchema,
        EMPTY_RUNTIME_AGENT_WORKSPACES_RESPONSE,
        { endpoint: "test runtime agent workspaces" },
      ),
    ).toEqual(EMPTY_RUNTIME_AGENT_WORKSPACES_RESPONSE);
  });
});

describe("WorkspaceSchema", () => {
  const workspace = {
    id: "workspace-1",
    name: "Multica",
    slug: "multica",
    description: null,
    context: null,
    settings: {},
    issue_prefix: "MUL",
    avatar_url: null,
    onboarding_agent_id: "agent-onboarding",
    created_at: "2026-08-05T00:00:00Z",
    updated_at: "2026-08-05T00:00:00Z",
  };

  it("parses workspace responses without repository metadata", () => {
    expect(WorkspaceSchema.parse(workspace)).toEqual(workspace);
    expect(WorkspaceListSchema.parse([workspace])).toEqual([workspace]);
  });

  it("preserves a missing onboarding binding from an older server", () => {
    const { onboarding_agent_id: _omitted, ...legacyWorkspace } = workspace;
    expect(WorkspaceSchema.parse(legacyWorkspace).onboarding_agent_id).toBeUndefined();
  });

  it("falls back when a workspace response is malformed", () => {
    expect(
      parseWithFallback({ ...workspace, id: 42 }, WorkspaceSchema, EMPTY_WORKSPACE, {
        endpoint: "test workspace",
      }),
    ).toEqual(EMPTY_WORKSPACE);
    expect(
      parseWithFallback({ workspaces: [] }, WorkspaceListSchema, [], {
        endpoint: "test workspace list",
      }),
    ).toEqual([]);
  });
});

describe("SandboxNodeTemplatesResponseSchema", () => {
  it("parses a well-formed templates payload", () => {
    const parsed = SandboxNodeTemplatesResponseSchema.parse({
      templates: [
        { template_id: "tpl-1", status: "READY", is_default: true },
        { template_id: "tpl-2", status: "BUILDING", image_info: "img" },
      ],
      default_template_id: "tpl-1",
      synced_at: "2026-07-16T08:00:00Z",
      node_online: true,
    });
    expect(parsed.templates).toHaveLength(2);
    expect(parsed.templates[0]?.template_id).toBe("tpl-1");
    expect(parsed.node_online).toBe(true);
  });

  it("falls back when templates is missing or wrong-typed", () => {
    const missing = parseWithFallback(
      { node_online: true },
      SandboxNodeTemplatesResponseSchema,
      EMPTY_SANDBOX_NODE_TEMPLATES_RESPONSE,
      { endpoint: "GET /api/sandbox/nodes/x/templates" },
    );
    expect(missing.templates).toEqual([]);

    const malformed = parseWithFallback(
      { templates: null },
      SandboxNodeTemplatesResponseSchema,
      EMPTY_SANDBOX_NODE_TEMPLATES_RESPONSE,
      { endpoint: "GET /api/sandbox/nodes/x/templates" },
    );
    expect(malformed).toEqual(EMPTY_SANDBOX_NODE_TEMPLATES_RESPONSE);
  });
});

describe("EnsureWindyResponseSchema", () => {
  it("requires the created or adopted Agent identity", () => {
    expect(EnsureWindyResponseSchema.parse({ agent: { id: "wendy-1" } }).agent.id).toBe("wendy-1");
    expect(() => EnsureWindyResponseSchema.parse({ agent: {} })).toThrow();
    expect(() => EnsureWindyResponseSchema.parse({ dm_id: "dm-1" })).toThrow();
  });
});

describe("EnsurePeriodBriefAgentResponseSchema", () => {
  it("requires agent.id and created", () => {
    expect(
      EnsurePeriodBriefAgentResponseSchema.parse({
        agent: { id: "weekly-1" },
        created: true,
      }).created,
    ).toBe(true);
    expect(() => EnsurePeriodBriefAgentResponseSchema.parse({ agent: { id: "weekly-1" } })).toThrow();
  });
});

describe("ConversationHandleLookupSchema", () => {
  it("accepts an authorized href", () => {
    expect(
      ConversationHandleLookupSchema.parse({
        available: true,
        href: "/acme/channels/chan-1?message=msg-1",
      }),
    ).toEqual({
      available: true,
      href: "/acme/channels/chan-1?message=msg-1",
    });
  });

  it("falls closed when the payload is malformed", () => {
    const missing = parseWithFallback(
      { href: "/acme/channels/chan-1" },
      ConversationHandleLookupSchema,
      EMPTY_CONVERSATION_HANDLE_LOOKUP,
      { endpoint: "GET /api/conversations/lookup" },
    );
    expect(missing).toEqual(EMPTY_CONVERSATION_HANDLE_LOOKUP);

    const wrongType = parseWithFallback(
      { available: "yes" },
      ConversationHandleLookupSchema,
      EMPTY_CONVERSATION_HANDLE_LOOKUP,
      { endpoint: "GET /api/conversations/lookup" },
    );
    expect(wrongType).toEqual(EMPTY_CONVERSATION_HANDLE_LOOKUP);
  });
});

describe("ChannelMentionCandidatesResponseSchema", () => {
  it("parses a canonical mention-candidates page", () => {
    const parsed = ChannelMentionCandidatesResponseSchema.parse({
      in_channel: [{ type: "agent", id: "a1", handle: "li-wei", label: "里维" }],
      not_in_channel: [{ type: "member", id: "u2", handle: "bob", label: "Bob" }],
      has_more: true,
      next_offset: 20,
    });
    expect(parsed.in_channel[0]?.label).toBe("里维");
    expect(parsed.has_more).toBe(true);
    expect(parsed.next_offset).toBe(20);
  });

  it("falls back when the body is malformed", () => {
    expect(
      parseWithFallback(null, ChannelMentionCandidatesResponseSchema, EMPTY_CHANNEL_MENTION_CANDIDATES, {
        endpoint: "GET /api/channels/{id}/mention-candidates",
      }),
    ).toEqual(EMPTY_CHANNEL_MENTION_CANDIDATES);
    const missing = parseWithFallback(
      { in_channel: null, not_in_channel: "x" },
      ChannelMentionCandidatesResponseSchema,
      EMPTY_CHANNEL_MENTION_CANDIDATES,
      { endpoint: "GET /api/channels/{id}/mention-candidates" },
    );
    expect(missing.in_channel).toEqual([]);
    expect(missing.not_in_channel).toEqual([]);
    expect(missing.has_more).toBe(false);
  });
});

describe("ComputerConnectionSchema", () => {
  it("keeps Journal enablement optional so older servers still parse", () => {
    expect(
      ComputerConnectionSchema.parse({
        daemon_id: "computer-1",
        owner_id: "user-1",
        connected: true,
        last_seen_at: null,
      }),
    ).toEqual({
      daemon_id: "computer-1",
      owner_id: "user-1",
      connected: true,
      last_seen_at: null,
    });
    expect(
      ComputerConnectionSchema.parse({
        daemon_id: "computer-1",
        owner_id: "user-1",
        connected: true,
        last_seen_at: "2026-08-17T00:00:00Z",
        deviceName: "ubuntu-build-host",
        os: "linux",
        cliVersion: "0.4.24-alpha.91",
        work_journal_enabled: true,
      }),
    ).toMatchObject({
      deviceName: "ubuntu-build-host",
      os: "linux",
      cliVersion: "0.4.24-alpha.91",
      work_journal_enabled: true,
    });
  });

  it("falls back the list when the body is malformed", () => {
    expect(
      parseWithFallback(null, ComputerConnectionListSchema, EMPTY_COMPUTER_CONNECTION_LIST, {
        endpoint: "GET /api/computers",
      }),
    ).toEqual(EMPTY_COMPUTER_CONNECTION_LIST);
  });
});

describe("ComputerCollectRootsSchema", () => {
  it("fails closed when roots is missing or the wrong type", () => {
    expect(ComputerCollectRootsSchema.parse({ roots: ["~/code"] })).toEqual({
      roots: ["~/code"],
    });
    expect(
      parseWithFallback(
        { roots: "yes" },
        ComputerCollectRootsSchema,
        EMPTY_COMPUTER_COLLECT_ROOTS,
        { endpoint: "GET /api/computers/:daemonId/collect-roots" },
      ),
    ).toEqual(EMPTY_COMPUTER_COLLECT_ROOTS);
  });
});

describe("ComputerWorkJournalSettingSchema", () => {
  it("fails closed when enabled is missing or the wrong type", () => {
    expect(ComputerWorkJournalSettingSchema.parse({ enabled: true })).toEqual({ enabled: true });
    expect(
      parseWithFallback(
        { enabled: "yes" },
        ComputerWorkJournalSettingSchema,
        EMPTY_COMPUTER_WORK_JOURNAL_SETTING,
        { endpoint: "PATCH /api/computers/:daemonId/work-journal" },
      ),
    ).toEqual(EMPTY_COMPUTER_WORK_JOURNAL_SETTING);
  });
});
