import { afterEach, describe, expect, it, vi } from "vitest";
import {
  EMPTY_GRAPH_MEMORY_AUDIT,
  MemoryExploreV2EvidenceSchema,
  MemoryExploreV2HistorySchema,
  MemoryExploreV2SearchResponseSchema,
  MemoryRefSchema,
  EMPTY_GRAPH_MEMORY_PROFILE,
  EMPTY_GRAPH_MEMORY_STATUS,
  GraphMemoryAuditSummarySchema,
  GraphMemoryChannelLineageSchema,
  GraphMemoryChannelModeSchema,
  GraphMemoryConsolidationRunSchema,
  GraphMemoryProfileSchema,
  GraphMemoryStatusSchema,
  MemoryRetentionPolicySchema,
  MemoryRetentionResponseSchema,
  TrainingGovernanceResponseSchema,
  type TrainingGovernanceResponse,
  TrainingGrantRevokeResponseSchema,
  graphMemoryCitationClass,
} from "./schemas";
import { ApiClient } from "./client";
import { parseWithFallback } from "./schema";

describe("GraphMemoryProfileSchema", () => {
  it("parses a Dive-Judge-era profile with all tunables", () => {
    const parsed = GraphMemoryProfileSchema.parse({
      workspace_id: "ws-1",
      memory_type: "graph",
      explore_agents: 4,
      explore_max_rounds: 3,
      ttt_enabled: true,
      explore_nodes_per_expansion: 2,
      max_hierarchy_fanout: 12,
      max_relation_edges_per_node: 6,
      dive_max_rounds: 8,
      dive_max_viewed_nodes: 48,
      dive_max_source_files: 6,
      dive_timeout_seconds: 900,
      w_round: 0.25,
      source_max_file_bytes: 10485760,
      source_max_total_bytes: 41943040,
      source_max_pdf_pages: 80,
      source_max_av_seconds: 300,
      source_max_image_megapixels: 24,
      dive_model: "judge-v2",
      dive_provider: "openai",
      config_version: 7,
      updated_at: "2026-08-18T01:00:00Z",
    });
    expect(parsed.ttt_enabled).toBe(true);
    expect(parsed.explore_nodes_per_expansion).toBe(2);
    expect(parsed.max_hierarchy_fanout).toBe(12);
    expect(parsed.max_relation_edges_per_node).toBe(6);
    expect(parsed.dive_max_rounds).toBe(8);
    expect(parsed.dive_max_viewed_nodes).toBe(48);
    expect(parsed.dive_max_source_files).toBe(6);
    expect(parsed.dive_timeout_seconds).toBe(900);
    expect(parsed.w_round).toBe(0.25);
    expect(parsed.source_max_file_bytes).toBe(10485760);
    expect(parsed.source_max_total_bytes).toBe(41943040);
    expect(parsed.source_max_pdf_pages).toBe(80);
    expect(parsed.source_max_av_seconds).toBe(300);
    expect(parsed.source_max_image_megapixels).toBe(24);
    expect(parsed.dive_model).toBe("judge-v2");
    expect(parsed.dive_provider).toBe("openai");
    expect(parsed.config_version).toBe(7);
  });

  it("defaults Dive-Judge tunables on a legacy-shaped payload", () => {
    const parsed = GraphMemoryProfileSchema.parse({
      workspace_id: "ws-1",
      memory_type: "legacy",
      explore_agents: 4,
      explore_max_rounds: 3,
    });
    expect(parsed.ttt_enabled).toBe(false);
    expect(parsed.explore_nodes_per_expansion).toBe(1);
    expect(parsed.max_hierarchy_fanout).toBe(8);
    expect(parsed.max_relation_edges_per_node).toBe(8);
    expect(parsed.dive_max_rounds).toBe(6);
    expect(parsed.dive_max_viewed_nodes).toBe(24);
    expect(parsed.dive_max_source_files).toBe(4);
    expect(parsed.dive_timeout_seconds).toBe(600);
    expect(parsed.w_round).toBe(0.1);
    expect(parsed.source_max_file_bytes).toBe(20971520);
    expect(parsed.source_max_total_bytes).toBe(52428800);
    expect(parsed.source_max_pdf_pages).toBe(50);
    expect(parsed.source_max_av_seconds).toBe(600);
    expect(parsed.source_max_image_megapixels).toBe(40);
    expect(parsed.dive_model).toBe("");
    expect(parsed.dive_provider).toBe("");
    expect(parsed.config_version).toBe(0);
  });

  it("rejects an unsupported memory_type instead of coercing to legacy", () => {
    const result = GraphMemoryProfileSchema.safeParse({
      workspace_id: "ws-1",
      memory_type: "weird",
    });
    expect(result.success).toBe(false);
  });

  it("surfaces malformed profiles through the fallback path", () => {
    const parsed = parseWithFallback(
      { workspace_id: "ws-1", memory_type: "weird" },
      GraphMemoryProfileSchema,
      EMPTY_GRAPH_MEMORY_PROFILE,
      { endpoint: "GET /api/workspaces/{id}/graph-memory/profile" },
    );
    expect(parsed).toEqual(EMPTY_GRAPH_MEMORY_PROFILE);
  });
});

describe("GraphMemoryStatusSchema", () => {
  it("parses a populated status", () => {
    const parsed = GraphMemoryStatusSchema.parse({
      workspace_id: "ws-1",
      memory_type: "graph",
      scoped_writer_ready: true,
      empty_start: false,
      graphs: [{
        kind: "project", owner_id: "p-1", current_version: 3, versions: [1, 2, 3],
        staging_segments: 2, last_consolidated_at: "2026-08-17T01:00:00Z",
        consolidation_backoff: false, recall_queries_24h: 10, recall_hit_rate_24h: 0.7,
      }],
    });
    expect(parsed.graphs[0]?.recall_hit_rate_24h).toBe(0.7);
  });

  it("defaults missing collections", () => {
    const parsed = GraphMemoryStatusSchema.parse({ memory_type: "legacy" });
    expect(parsed.memory_type).toBe("legacy");
    expect(parsed.graphs).toEqual([]);
    expect(parsed.empty_start).toBe(true);
  });

  it("rejects an unsupported memory_type instead of coercing to legacy", () => {
    const result = GraphMemoryStatusSchema.safeParse({ memory_type: "weird" });
    expect(result.success).toBe(false);
  });

  it("falls back on a malformed response", () => {
    const parsed = parseWithFallback("not json", GraphMemoryStatusSchema, EMPTY_GRAPH_MEMORY_STATUS, {
      endpoint: "GET /api/workspaces/{id}/graph-memory/status",
    });
    expect(parsed).toEqual(EMPTY_GRAPH_MEMORY_STATUS);
  });
});

describe("GraphMemoryAuditSummarySchema", () => {
  it("defaults counters on a partial payload", () => {
    const parsed = GraphMemoryAuditSummarySchema.parse({ workspace_id: "ws-1" });
    expect(parsed.queries_24h).toBe(0);
    expect(parsed.recall_hit_rate_24h).toBe(0);
  });

  it("falls back on malformed payloads", () => {
    const parsed = parseWithFallback(42, GraphMemoryAuditSummarySchema, EMPTY_GRAPH_MEMORY_AUDIT, {
      endpoint: "GET /api/workspaces/{id}/graph-memory/audit",
    });
    expect(parsed).toEqual(EMPTY_GRAPH_MEMORY_AUDIT);
  });
});

describe("GraphMemoryChannelLineageSchema", () => {
  it("accepts a channel with no route yet", () => {
    const parsed = GraphMemoryChannelLineageSchema.parse({
      workspace_id: "ws-1", channel_id: "c-1", routing_mode: "", current: null, lineage: [],
    });
    expect(parsed.current).toBeNull();
    expect(parsed.lineage).toEqual([]);
  });
});

describe("GraphMemoryConsolidationRunSchema", () => {
  it("defaults optional timestamps", () => {
    const parsed = GraphMemoryConsolidationRunSchema.parse({
      id: "r-1", workspace_id: "ws-1", status: "queued", trigger_kind: "manual",
    });
    expect(parsed.started_at).toBe("");
    expect(parsed.finished_at).toBe("");
  });
});

describe("GraphMemoryChannelModeSchema", () => {
  const base = {
    workspace_id: "ws-1",
    channel_id: "channel-1",
    override: "agent",
    effective_mode: "agent",
    status: "active",
    blocked_reason: "",
    agent_id: "agent-1",
    runtime_id: "runtime-actual",
  };

  it("parses channel override, effective config, and actual binding separately", () => {
    const parsed = GraphMemoryChannelModeSchema.parse({
      ...base,
      memory_agent_runtime_id_override: "runtime-channel",
      memory_agent_model_override: "channel/model",
      memory_agent_thinking_override: "high",
      effective_memory_agent_runtime_id: "runtime-channel",
      effective_memory_agent_model: "channel/model",
      effective_memory_agent_thinking: "high",
    });
    expect(parsed.runtime_id).toBe("runtime-actual");
    expect(parsed.memory_agent_runtime_id_override).toBe("runtime-channel");
    expect(parsed.effective_memory_agent_model).toBe("channel/model");
  });

  it("defaults new fields for legacy server payloads", () => {
    const parsed = GraphMemoryChannelModeSchema.parse(base);
    expect(parsed.memory_agent_runtime_id_override).toBe("");
    expect(parsed.effective_memory_agent_runtime_id).toBe("");
  });
});

describe("MemoryExploreV2 schemas", () => {
  it("parses a structured search response and rejects raw ref maps", () => {
    const parsed = MemoryExploreV2SearchResponseSchema.parse({
      workspace_id: "ws-1",
      hits: [
        {
          ref: { kind: "staging_atom", atom_id: "atom-1", segment_id: "seg-1" },
          class: "staging_atom",
          score: 0.42,
        },
      ],
    });
    expect(parsed.hits[0]?.ref.atom_id).toBe("atom-1");
    expect(parsed.hits[0]?.ref.kind).toBe("staging_atom");

    // An unvalidated raw ref map (unknown kind or extra shape) must fail
    // strict parsing rather than coerce.
    expect(() =>
      MemoryExploreV2SearchResponseSchema.parse({
        workspace_id: "ws-1",
        hits: [{ ref: { kind: "raw_map", anything: { nested: true } }, class: "staging_atom", score: 1 }],
      }),
    ).toThrow();
    expect(() =>
      MemoryRefSchema.parse({ kind: "graph_node", atom_id: "a1" }),
    ).not.toThrow(); // server-side validation rejects; wire shape stays parseable
  });

  it("defaults empty search hits and evidence fields", () => {
    const search = MemoryExploreV2SearchResponseSchema.parse({ workspace_id: "ws-1" });
    expect(search.hits).toEqual([]);
    const evidence = MemoryExploreV2EvidenceSchema.parse({
      ref: { kind: "staging_atom", atom_id: "a", segment_id: "s" },
      segment_id: "s",
    });
    expect(evidence.summary).toBe("");
    expect(evidence.publish_seq).toBe(0);
    const history = MemoryExploreV2HistorySchema.parse({ trajectory_id: "t-1" });
    expect(history.refs).toEqual([]);
  });

  it("parses evidence with a bounded trajectory chunk", () => {
    const evidence = MemoryExploreV2EvidenceSchema.parse({
      ref: { kind: "staging_atom", atom_id: "a", segment_id: "s" },
      segment_id: "s",
      summary: "closing event text",
      trajectory_chunk: { sanitized: true },
      publish_seq: 7,
    });
    expect(evidence.summary).toBe("closing event text");
    expect(evidence.publish_seq).toBe(7);
  });
});

describe("graphMemoryCitationClass", () => {
  const citation = (overrides: Partial<Parameters<typeof graphMemoryCitationClass>[0]>) => ({
    level: "1",
    epistemic_status: "",
    title: "Node title",
    first_paragraph: "Node body",
    excerpt: "Node body",
    ...overrides,
  });

  it("classes a content-bearing graph node as consolidated", () => {
    expect(graphMemoryCitationClass(citation({}))).toBe("consolidated");
  });

  it("classes level -1 staging citations as recent-unreviewed", () => {
    expect(
      graphMemoryCitationClass(citation({ level: "-1", title: "", first_paragraph: "Raw observation", excerpt: "Raw observation" })),
    ).toBe("recent-unreviewed");
  });

  it("classes superseded epistemic status as historical", () => {
    expect(graphMemoryCitationClass(citation({ epistemic_status: "superseded" }))).toBe("historical");
  });

  it("classes a graph node whose content was withheld as restricted", () => {
    expect(graphMemoryCitationClass(citation({ title: "", first_paragraph: "", excerpt: "" }))).toBe("restricted");
  });

  it("classes the fail-closed retraction sentinel as retracted ahead of every other rule", () => {
    expect(
      graphMemoryCitationClass(citation({ epistemic_status: "superseded", excerpt: "content_retracted" })),
    ).toBe("retracted");
    expect(
      graphMemoryCitationClass(citation({ level: "-1", title: "", first_paragraph: "content_retracted", excerpt: "" })),
    ).toBe("retracted");
  });
});

describe("TrainingGovernance schemas", () => {
  it("parses a pending_owner_ack grant with the global switches", () => {
    const parsed = TrainingGovernanceResponseSchema.parse({
      grant: {
        grant_id: "grant-1",
        workspace_id: "ws-1",
        tenant_status: "pending_owner_ack",
        tenant_policy_version: 3,
        tenant_granted_by: "user:owner",
        tenant_granted_at: "2026-08-20T00:00:00Z",
        pooled_status: "disabled",
        pooled_policy_version: 0,
        pooled_granted_by: "",
        pooled_granted_at: "",
      },
      policy: {
        selection_enabled: false,
        execution_enabled: false,
        reward_policy_version: 2,
        per_agent_sample_cap: 200,
        per_channel_sample_cap: 1000,
        per_workspace_sample_cap: 5000,
      },
    });
    expect(parsed.grant.tenant_status).toBe("pending_owner_ack");
    expect(parsed.grant.tenant_policy_version).toBe(3);
    expect(parsed.policy.selection_enabled).toBe(false);
    expect(parsed.policy.execution_enabled).toBe(false);
    expect(parsed.policy.per_workspace_sample_cap).toBe(5000);
  });

  it("parses the revoke report with deletion ledger counters", () => {
    const parsed = TrainingGrantRevokeResponseSchema.parse({
      workspace_id: "ws-1",
      purpose: "tenant",
      invalidated: 2,
      revoked_samples: 12,
      deletion_ledger_rows: 5,
    });
    expect(parsed.invalidated).toBe(2);
    expect(parsed.revoked_samples).toBe(12);
    expect(parsed.deletion_ledger_rows).toBe(5);
  });
});

describe("MemoryRetention schemas", () => {
  it("parses the bootstrap 90/365/30 policy with the platform caps", () => {
    const parsed = MemoryRetentionResponseSchema.parse({
      policy: { version: 1, trajectory_hot_days: 90, archive_days: 365, trace_hot_days: 30 },
      caps: { trajectory_hot_days: 90, archive_days: 365, trace_hot_days: 30 },
    });
    expect(parsed.policy.version).toBe(1);
    expect(parsed.policy.trajectory_hot_days).toBe(90);
    expect(parsed.policy.archive_days).toBe(365);
    expect(parsed.policy.trace_hot_days).toBe(30);
    expect(parsed.caps.archive_days).toBe(365);
  });

  it("rejects negative retention windows instead of coercing them", () => {
    const result = MemoryRetentionPolicySchema.safeParse({
      version: 1,
      trajectory_hot_days: -1,
      archive_days: 365,
      trace_hot_days: 30,
    });
    expect(result.success).toBe(false);
  });
});

describe("GraphMemoryChannelLineageSchema migration", () => {
  it("parses the latest migration binding progress", () => {
    const parsed = GraphMemoryChannelLineageSchema.parse({
      workspace_id: "ws-1",
      channel_id: "c-1",
      routing_mode: "project_lineage",
      current: null,
      lineage: [],
      migration: { binding_generation: 3, source_watermark: 1200, phase: "copying", copied_atoms: 42 },
    });
    expect(parsed.migration?.binding_generation).toBe(3);
    expect(parsed.migration?.source_watermark).toBe(1200);
    expect(parsed.migration?.phase).toBe("copying");
    expect(parsed.migration?.copied_atoms).toBe(42);
  });

  it("keeps migration null for channels that never rebound across projects", () => {
    const parsed = GraphMemoryChannelLineageSchema.parse({
      workspace_id: "ws-1", channel_id: "c-1", routing_mode: "", current: null, lineage: [],
    });
    expect(parsed.migration).toBeNull();
  });
});

describe("GraphMemoryAuditSummarySchema ledger", () => {
  it("parses ledger failure counters for pipeline health", () => {
    const parsed = GraphMemoryAuditSummarySchema.parse({
      workspace_id: "ws-1",
      queries_24h: 10,
      ledger: {
        recalls_by_status: { hit: 7, miss: 3 },
        recalls_by_error_kind: { provider_timeout: 2 },
        trajectories_by_status: { terminal: 5 },
        trajectories_by_dive_status: { graded: 4 },
        avg_rounds: 2.5,
        p95_rounds: 4,
        graded_trajectories: 4,
        overall_reward_min: -1,
        overall_reward_avg: 0.5,
        dive_jobs_by_status: { succeeded: 3, failed: 1 },
        dive_job_attempts: 4,
        last_failure: { kind: "dive_model", message: "provider 500" },
        reward_outbox_by_status: { delivered: 5, failed: 1 },
        oldest_pending_age_seconds: 61.5,
        offline_export_eligible: 2,
        catalog_items: 3,
      },
    });
    expect(parsed.ledger.recalls_by_error_kind.provider_timeout).toBe(2);
    expect(parsed.ledger.reward_outbox_by_status.failed).toBe(1);
    expect(parsed.ledger.dive_jobs_by_status.failed).toBe(1);
    expect(parsed.ledger.oldest_pending_age_seconds).toBe(61.5);
  });

  it("defaults the ledger for legacy payloads without one", () => {
    const parsed = GraphMemoryAuditSummarySchema.parse({ workspace_id: "ws-1" });
    expect(parsed.ledger.recalls_by_error_kind).toEqual({});
    expect(parsed.ledger.reward_outbox_by_status).toEqual({});
    expect(parsed.ledger.dive_jobs_by_status).toEqual({});
    expect(parsed.ledger.dive_job_attempts).toBe(0);
  });
});

describe("training and retention client methods", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  const jsonResponse = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

  it("loads the training governance pair", async () => {
    const response = {
      grant: { grant_id: "grant-1", workspace_id: "ws-1", tenant_status: "active", tenant_policy_version: 2, pooled_status: "disabled", pooled_policy_version: 0 },
      policy: { selection_enabled: true, execution_enabled: false, reward_policy_version: 2, per_agent_sample_cap: 200, per_channel_sample_cap: 1000, per_workspace_sample_cap: 5000 },
    };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(response));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    const parsed = await client.getTrainingGovernance("ws-1");
    expect(parsed.grant.tenant_status).toBe("active");
    expect(parsed.policy.selection_enabled).toBe(true);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/workspaces/ws-1/training/grant",
      expect.anything(),
    );
  });

  it("acks the tenant grant with the seen CAS version", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({
      // PUT answers with the same envelope as GET; policy marshals as the
      // zero-value struct when the action does not touch it.
      grant: { grant_id: "grant-1", workspace_id: "ws-1", tenant_status: "active", tenant_policy_version: 5 },
      policy: { selection_enabled: false, execution_enabled: false, reward_policy_version: 0, per_agent_sample_cap: 0, per_channel_sample_cap: 0, per_workspace_sample_cap: 0 },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    const parsed = await client.updateTrainingGrant("ws-1", { purpose: "tenant", action: "ack", expected_version: 4 });
    expect((parsed as TrainingGovernanceResponse).grant.tenant_policy_version).toBe(5);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/workspaces/ws-1/training/grant",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ purpose: "tenant", action: "ack", expected_version: 4 }),
      }),
    );
  });

  it("updates the global training policy switches with a sparse patch", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({
      policy: { selection_enabled: true, execution_enabled: false, reward_policy_version: 2, per_agent_sample_cap: 200, per_channel_sample_cap: 1000, per_workspace_sample_cap: 5000 },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    const parsed = await client.updateTrainingPolicy("ws-1", { selection_enabled: true });
    expect(parsed.selection_enabled).toBe(true);
    expect(fetchMock).toHaveBeenCalledWith(
      "https://api.example.test/api/workspaces/ws-1/training/policy",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ selection_enabled: true }),
      }),
    );
  });

  it("loads the memory retention policy and saves a shortened window", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({
        policy: { version: 1, trajectory_hot_days: 90, archive_days: 365, trace_hot_days: 30 },
        caps: { trajectory_hot_days: 90, archive_days: 365, trace_hot_days: 30 },
      }))
      .mockResolvedValueOnce(jsonResponse({
        policy: { version: 2, trajectory_hot_days: 90, archive_days: 180, trace_hot_days: 30 },
        caps: { trajectory_hot_days: 90, archive_days: 365, trace_hot_days: 30 },
      }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    const current = await client.getMemoryRetention("ws-1");
    expect(current.policy.archive_days).toBe(365);
    const saved = await client.updateMemoryRetention("ws-1", {
      trajectory_hot_days: 90,
      archive_days: 180,
      trace_hot_days: 30,
      expected_version: 1,
    });
    expect(saved.policy.version).toBe(2);
    expect(saved.policy.archive_days).toBe(180);
    expect(fetchMock).toHaveBeenNthCalledWith(2,
      "https://api.example.test/api/workspaces/ws-1/memory/retention",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ trajectory_hot_days: 90, archive_days: 180, trace_hot_days: 30, expected_version: 1 }),
      }),
    );
  });

  it("lets PUT failures surface instead of swallowing them into a fallback", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ error: "retention policy version conflict" }, 409));
    vi.stubGlobal("fetch", fetchMock);
    const client = new ApiClient("https://api.example.test");

    await expect(
      client.updateMemoryRetention("ws-1", { trajectory_hot_days: 90, archive_days: 90, trace_hot_days: 30, expected_version: 0 }),
    ).rejects.toMatchObject({ status: 409 });
  });
});
