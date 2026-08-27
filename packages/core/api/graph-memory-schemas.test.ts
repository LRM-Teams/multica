import { describe, expect, it } from "vitest";
import {
  EMPTY_GRAPH_MEMORY_AUDIT,
  EMPTY_GRAPH_MEMORY_PROFILE,
  EMPTY_GRAPH_MEMORY_STATUS,
  GraphMemoryAuditSummarySchema,
  GraphMemoryChannelLineageSchema,
  GraphMemoryChannelModeSchema,
  GraphMemoryConsolidationRunSchema,
  GraphMemoryProfileSchema,
  GraphMemoryStatusSchema,
} from "./schemas";
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
