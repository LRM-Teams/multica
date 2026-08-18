import { describe, expect, it } from "vitest";
import {
  EMPTY_GRAPH_MEMORY_AUDIT,
  EMPTY_GRAPH_MEMORY_STATUS,
  GraphMemoryAuditSummarySchema,
  GraphMemoryChannelLineageSchema,
  GraphMemoryConsolidationRunSchema,
  GraphMemoryStatusSchema,
} from "./schemas";
import { parseWithFallback } from "./schema";

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

  it("defaults missing collections and catches bad enums", () => {
    const parsed = GraphMemoryStatusSchema.parse({ memory_type: "weird" });
    expect(parsed.memory_type).toBe("legacy");
    expect(parsed.graphs).toEqual([]);
    expect(parsed.empty_start).toBe(true);
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
